/** 将不受信任的值编码为HTML文本，禁止资产内容形成标签或事件属性。 */
export function escapeHTML(value) {
    return String(value ?? '')
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&#39;');
}

export function displayText(value, fallback = '-') {
    if (value === null || value === undefined || value === '') {
        return fallback;
    }
    return escapeHTML(value);
}
