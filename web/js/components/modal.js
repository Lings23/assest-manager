/**
 * 模态框组件
 */

/**
 * 打开模态框
 * @param {string} title - 模态框标题
 * @param {string} content - 模态框内容HTML
 * @param {function} onSave - 保存按钮回调
 */
export function openModal(title, content, onSave) {
    document.getElementById('modal-title').textContent = title;
    document.getElementById('modal-body').innerHTML = content;
    document.getElementById('modal-save-btn').onclick = onSave;
    document.getElementById('modal').classList.add('show');
}

/**
 * 关闭模态框
 */
export function closeModal() {
    document.getElementById('modal').classList.remove('show');
}

/**
 * 获取模态框模板HTML
 * @returns {string}
 */
export function getModalTemplate() {
    return `
    <div id="modal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <h3 id="modal-title">标题</h3>
                <button class="close-btn" onclick="closeModal()">&times;</button>
            </div>
            <div class="modal-body" id="modal-body">
                <!-- 动态内容 -->
            </div>
            <div class="modal-footer">
                <button class="btn-sm" onclick="closeModal()">取消</button>
                <button class="btn-sm btn-primary" id="modal-save-btn">保存</button>
            </div>
        </div>
    </div>`;
}