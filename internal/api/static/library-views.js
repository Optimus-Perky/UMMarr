// Library views and the mass editor, as in Radarr's and Sonarr's index:
// Posters, Overview or Table (each with its own Options dialog, the choice
// remembered per browser), and Select mode, where ticking items opens a bar
// with Edit, Monitoring, Search and Delete for all of them at once.
(function () {
  var DIALOGS = {posters: 'poster-options', overview: 'overview-options', table: 'table-options'};
  function load(key, fallback) { try { var v = JSON.parse(localStorage.getItem(key) || 'null'); return v === null ? fallback : v; } catch (e) { return fallback; } }
  function save(key, value) { try { localStorage.setItem(key, JSON.stringify(value)); } catch (e) {} }

  window.ummarrLibraryOptions = function (library) {
    var grid = document.querySelector('.poster-grid[data-library="' + library + '"]');
    var dialog = document.getElementById(DIALOGS[(grid && grid.dataset.view) || 'posters'] || 'poster-options');
    if (dialog) dialog.showModal();
  };

  document.querySelectorAll('.poster-grid[data-library]').forEach(function (grid) {
    var name = grid.dataset.library;
    var toolbar = document.querySelector('.library-toolbar[data-library="' + name + '"]');
    if (!toolbar) return;
    var viewSelect = toolbar.querySelector('.library-view'), VIEW = 'ummarr-' + name + '-view';
    function setView(view) {
      if (!DIALOGS[view]) view = 'posters';
      grid.dataset.view = view;
      if (viewSelect) viewSelect.value = view;
      save(VIEW, view);
    }
    setView(load(VIEW, 'posters'));
    if (viewSelect) viewSelect.addEventListener('change', function () { setView(viewSelect.value); });

    // Table headings sort through the toolbar's own Sort control.
    grid.addEventListener('click', function (e) {
      var head = e.target.closest('.table-head [data-sort]');
      if (!head) return;
      var sortSelect = toolbar.querySelector('.library-sort'), dir = toolbar.querySelector('.library-dir');
      if (!sortSelect) return;
      if (sortSelect.value === head.dataset.sort) { if (dir) dir.click(); return; }
      sortSelect.value = head.dataset.sort;
      sortSelect.dispatchEvent(new Event('change'));
    });

    var bar = document.querySelector('.editor-bar[data-library="' + name + '"]');
    var toggle = toolbar.querySelector('.library-select-toggle');
    var selection = document.getElementById(name + '-selection');
    if (!bar || !toggle) return;
    var last = null;
    function checks() { return Array.prototype.slice.call(grid.querySelectorAll('.card-check')); }
    function sync() {
      var ids = checks().filter(function (c) { return c.checked; }).map(function (c) { return c.value; });
      var inputs = ids.map(function (id) { return '<input type="hidden" name="id" value="' + id + '">'; }).join('');
      bar.querySelector('.editor-count').textContent = ids.length + ' selected';
      if (selection) selection.innerHTML = inputs;
      document.querySelectorAll('[data-editor-for="' + name + '"] .editor-ids').forEach(function (slot) { slot.innerHTML = inputs; });
      document.querySelectorAll('[data-editor-for="' + name + '"] .editor-summary').forEach(function (p) { p.textContent = ids.length + ' selected'; });
      bar.querySelectorAll('[data-needs-selection]').forEach(function (b) { b.disabled = ids.length === 0; });
      checks().forEach(function (c) { c.closest('.card').classList.toggle('card-selected', c.checked); });
    }
    function setMode(on) {
      grid.classList.toggle('selecting', on);
      bar.hidden = !on;
      toggle.classList.toggle('active', on);
      toggle.textContent = on ? 'Stop selecting' : 'Select';
      if (!on) checks().forEach(function (c) { c.checked = false; });
      sync();
    }
    toggle.addEventListener('click', function () { setMode(!grid.classList.contains('selecting')); });
    // While selecting, clicking anywhere on an item ticks it (shift-click
    // ticks a range) instead of opening it.
    grid.addEventListener('click', function (e) {
      if (!grid.classList.contains('selecting')) return;
      var card = e.target.closest('.card');
      var box = card && card.querySelector('.card-check');
      if (!box) return;
      if (e.target !== box) {
        e.preventDefault();
        e.stopPropagation();
        box.checked = !box.checked;
      }
      if (e.shiftKey && last && last !== box) {
        var visible = checks().filter(function (c) { return !c.closest('.card').hidden; });
        var a = visible.indexOf(last), b = visible.indexOf(box);
        if (a >= 0 && b >= 0) visible.slice(Math.min(a, b), Math.max(a, b) + 1).forEach(function (c) { c.checked = box.checked; });
      }
      last = box;
      sync();
    }, true);
    bar.addEventListener('click', function (e) {
      var b = e.target.closest('[data-editor]');
      if (!b) return;
      if (b.dataset.editor === 'all') {
        checks().forEach(function (c) { if (!c.closest('.card').hidden) c.checked = true; });
        sync();
      } else if (b.dataset.editor === 'none') {
        checks().forEach(function (c) { c.checked = false; });
        sync();
      } else {
        var dialog = document.getElementById(name + '-editor-' + b.dataset.editor);
        if (dialog) { sync(); dialog.showModal(); }
      }
    });
    setMode(false);
  });
})();
