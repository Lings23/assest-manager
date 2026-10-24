package taskcenter

import (
	"asset-platform/internal/servicekit"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type ObjectStore interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) (io.ReadCloser, string, error)
	Delete(context.Context, string) error
}
type S3Store struct {
	endpoint, accessKey, secretKey, bucket, region string
	retentionDays                                  int
	client                                         *http.Client
}

func NewS3Store(endpoint, accessKey, secretKey, bucket string, retentionDays int, observation ...*servicekit.Metrics) (*S3Store, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return nil, fmt.Errorf("invalid S3 configuration")
	}
	if retentionDays < 1 || retentionDays > 3650 {
		return nil, fmt.Errorf("S3 retention days must be between 1 and 3650")
	}
	var metrics *servicekit.Metrics
	if len(observation) > 0 {
		metrics = observation[0]
	}
	return &S3Store{endpoint: u.String(), accessKey: accessKey, secretKey: secretKey, bucket: bucket, region: "us-east-1", retentionDays: retentionDays, client: &http.Client{Timeout: 2 * time.Minute, Transport: servicekit.ObservedTransport(nil, metrics, "object_store")}}, nil
}
func (s *S3Store) Ensure(ctx context.Context) error {
	req, err := s.request(ctx, "PUT", s.bucket, "", "", nil, "application/octet-stream")
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 409 {
		return fmt.Errorf("create bucket returned %d", res.StatusCode)
	}
	return s.ensureLifecycle(ctx)
}
func (s *S3Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	req, err := s.request(ctx, "PUT", s.bucket, key, "", data, contentType)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("put object returned %d", res.StatusCode)
	}
	return nil
}
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	req, err := s.request(ctx, "GET", s.bucket, key, "", nil, "")
	if err != nil {
		return nil, "", err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, "", fmt.Errorf("get object returned %d", res.StatusCode)
	}
	return res.Body, res.Header.Get("Content-Type"), nil
}
func (s *S3Store) Delete(ctx context.Context, key string) error {
	req, err := s.request(ctx, "DELETE", s.bucket, key, "", nil, "")
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete object returned %d", res.StatusCode)
	}
	return nil
}
func (s *S3Store) ensureLifecycle(ctx context.Context) error {
	body := []byte(fmt.Sprintf(`<LifecycleConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Rule><ID>expire-import-sources</ID><Status>Enabled</Status><Filter><Prefix>imports/</Prefix></Filter><Expiration><Days>%d</Days></Expiration></Rule><Rule><ID>expire-import-errors</ID><Status>Enabled</Status><Filter><Prefix>import-errors/</Prefix></Filter><Expiration><Days>%d</Days></Expiration></Rule><Rule><ID>expire-exports</ID><Status>Enabled</Status><Filter><Prefix>exports/</Prefix></Filter><Expiration><Days>%d</Days></Expiration></Rule></LifecycleConfiguration>`, s.retentionDays, s.retentionDays, s.retentionDays))
	req, err := s.request(ctx, "PUT", s.bucket, "", "lifecycle=", body, "application/xml")
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("configure bucket lifecycle returned %d", res.StatusCode)
	}
	return nil
}
func (s *S3Store) request(ctx context.Context, method, bucket, key, canonicalQuery string, body []byte, contentType string) (*http.Request, error) {
	objectPath := "/" + url.PathEscape(bucket)
	if key != "" {
		parts := strings.Split(path.Clean("/"+key), "/")[1:]
		for i := range parts {
			parts[i] = url.PathEscape(parts[i])
		}
		objectPath += "/" + strings.Join(parts, "/")
	}
	payloadHash := sha256Hex(body)
	target := s.endpoint + objectPath
	if canonicalQuery != "" {
		target += "?" + canonicalQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	date := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	host := req.URL.Host
	contentMD5 := ""
	canonicalHeaders := ""
	signedHeaders := ""
	if canonicalQuery == "lifecycle=" {
		sum := md5.Sum(body) // S3 lifecycle configuration requires the RFC 1864 checksum.
		contentMD5 = base64.StdEncoding.EncodeToString(sum[:])
		canonicalHeaders += "content-md5:" + contentMD5 + "\n"
		signedHeaders = "content-md5;"
	}
	canonicalHeaders += "host:" + host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + amzDate + "\n"
	signedHeaders += "host;x-amz-content-sha256;x-amz-date"
	canonical := method + "\n" + objectPath + "\n" + canonicalQuery + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + payloadHash
	scope := date + "/" + s.region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	dateKey := hmacSHA([]byte("AWS4"+s.secretKey), date)
	regionKey := hmacSHA(dateKey, s.region)
	serviceKey := hmacSHA(regionKey, "s3")
	signingKey := hmacSHA(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA(signingKey, stringToSign))
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if contentMD5 != "" {
		req.Header.Set("Content-MD5", contentMD5)
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.accessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}
func sha256Hex(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func hmacSHA(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
