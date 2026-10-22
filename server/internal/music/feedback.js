'use strict';

const notificationRegion = document.createElement('div');
notificationRegion.id = 'notifications';
notificationRegion.setAttribute('aria-live', 'polite');
document.body.append(notificationRegion);
const editorError = document.createElement('p');
editorError.id = 'editorError';
editorError.className = 'form-error';
editorError.setAttribute('role', 'alert');
editorError.hidden = true;
$('editor').querySelector('.modal-foot').before(editorError);

function clearEditorError() { editorError.hidden = true; editorError.textContent = ''; }
$('editor').addEventListener('close', () => {clearEditorError();$('modalBody').querySelectorAll('input[type="password"]').forEach(input=>input.value='');state.saveAction=null});
$('secretDialog').addEventListener('close',()=>{$('secretValue').textContent=''});
function notifyStatus(message, type) {
  if (!state.user || !message || !['error', 'ok'].includes(type)) return;
  if (type === 'error' && $('editor').open) {
    editorError.textContent = message;
    editorError.hidden = false;
    return;
  }
  if (type === 'ok' && ['已连接', '已登录', '设置已读取', '统计已刷新'].includes(message)) return;
  if ([...notificationRegion.children].some(item => item.firstChild.textContent === message)) return;
  const item = document.createElement('div');
  item.className = 'notification ' + type;
  const text = document.createElement('span');
  text.textContent = message;
  const close = document.createElement('button');
  close.textContent = '×'; close.setAttribute('aria-label', '关闭提示');
  close.onclick = () => item.remove();
  item.append(text, close); notificationRegion.append(item);
  if (type === 'ok') setTimeout(() => item.remove(), 3000);
}

// Menus remain usable without a mouse; Escape also restores focus to the opener.
document.addEventListener('keydown', event => {
  const menu = document.querySelector('.context-submenu:not([hidden])') || (!$('contextMenu').hidden ? $('contextMenu') : null);
  if (!menu) return;
  const buttons = [...menu.querySelectorAll('button:not(:disabled)')];
  if (['ArrowDown', 'ArrowUp'].includes(event.key) && buttons.length) {
    event.preventDefault();
    const index = buttons.indexOf(document.activeElement);
    buttons[(index + (event.key === 'ArrowDown' ? 1 : buttons.length - 1)) % buttons.length].focus();
  }
});
