package main

import "testing"

func TestMissingSharedSigningKeyFailsUnlessDevelopmentExplicitlyEnabled(t *testing.T) {
	t.Setenv("IAM_PRIVATE_KEY_FILE", "")
	t.Setenv("IAM_PRIVATE_KEY_PEM", "")
	t.Setenv("IAM_ALLOW_EPHEMERAL_KEY", "")
	if _, _, err := loadPrivateKey(); err == nil {
		t.Fatal("missing shared key accepted")
	}
	t.Setenv("IAM_ALLOW_EPHEMERAL_KEY", "true")
	if key, ephemeral, err := loadPrivateKey(); err != nil || !ephemeral || key == nil {
		t.Fatal("explicit development mode failed", err)
	}
}
