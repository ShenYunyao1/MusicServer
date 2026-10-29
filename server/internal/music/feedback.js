'use strict';

// Light dismissal for current and dynamically created dialogs. The backdrop
// targets the dialog itself, so also check coordinates to keep padding clickable.
(() => {
  let press = null;
  const outside = (dialog, event) => {
    const box = dialog.getBoundingClientRect();
    return event.clientX < box.left || event.clientX > box.right ||
      event.clientY < box.top || event.clientY > box.bottom;
  };
  document.addEventListener('pointerdown', event => {
    press = null;
    const dialog = event.target;
    if (event.button !== 0 || !event.isPrimary ||
        !(dialog instanceof HTMLDialogElement) || !dialog.open ||
        !outside(dialog, event)) return;
    press = {dialog, id:event.pointerId, x:event.clientX, y:event.clientY, released:false};
  }, true);
  document.addEventListener('pointerup', event => {
    if (!press) return;
    if (event.pointerId !== press.id || event.target !== press.dialog ||
        !outside(press.dialog, event) ||
        Math.hypot(event.clientX - press.x, event.clientY - press.y) > 8) {
      press = null;
    } else press.released = true;
  }, true);
  document.addEventListener('pointercancel', () => {press = null}, true);
  document.addEventListener('click', event => {
    const started = press; press = null;
    if (!started?.released || event.button !== 0 || event.target !== started.dialog ||
        !started.dialog.open || !outside(started.dialog, event)) return;
    const dialog = started.dialog;
    // These credentials cannot be recovered after closing their one-time view.
    if (['secretDialog', 'temporaryPasswordDialog'].includes(dialog.id)) return;
    if (dialog.querySelector('.modal-head button:disabled, .quota-head button:disabled')) return;
    event.preventDefault(); event.stopPropagation();
    // Use the Escape cancellation path, including unsaved-change and busy guards.
    if (dialog.dispatchEvent(new Event('cancel', {cancelable:true})) && dialog.open) dialog.close();
  }, true);
})();

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
$('secretDialog').addEventListener('cancel',()=>{$('secretValue').textContent=''});
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
