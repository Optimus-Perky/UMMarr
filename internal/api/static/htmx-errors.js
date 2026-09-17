// htmx 2 doesn't swap error responses, so a 422 "Couldn't add" or "Couldn't
// grab" message from UMMarr never appeared and the click looked dead. Swap
// 422 responses into their target like a success - except where the target
// is the whole page (some Settings forms re-render the body), where the
// message goes right after the form or button that was used instead.
document.addEventListener('htmx:beforeSwap', function (e) {
  var xhr = e.detail.xhr;
  if (!xhr || xhr.status !== 422) return;
  var target = e.detail.target;
  if (target && target !== document.body && target !== document.documentElement) {
    e.detail.shouldSwap = true;
    e.detail.isError = false;
    return;
  }
  var elt = e.detail.elt;
  if (!elt || !elt.parentNode) return;
  var next = elt.nextElementSibling;
  if (next && next.classList.contains('htmx-error-message')) next.remove();
  var box = document.createElement('div');
  box.className = 'htmx-error-message';
  box.innerHTML = xhr.responseText;
  elt.insertAdjacentElement('afterend', box);
});
