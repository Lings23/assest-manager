/**
 * 分页组件
 */

/**
 * 渲染分页控件
 * @param {number} total - 总记录数
 * @param {number} pageSize - 每页记录数
 * @param {number} currentPage - 当前页码
 * @param {function} onPageChange - 页码变化回调
 */
export function renderPagination(total, pageSize, currentPage, onPageChange) {
    const totalPages = Math.ceil(total / pageSize);
    const container = document.getElementById('pagination');

    if (totalPages <= 1) {
        container.innerHTML = '';
        return;
    }

    let html = '';
    for (let i = 1; i <= totalPages; i++) {
        html += `<button class="${i === currentPage ? 'active' : ''}" onclick="window.changePage(${i})">${i}</button>`;
    }
    container.innerHTML = html;

    // 设置全局分页回调
    window.changePage = onPageChange;
}