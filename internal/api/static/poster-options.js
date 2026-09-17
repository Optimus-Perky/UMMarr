// Poster Options, Overview Options and Table Options, as in Radarr and
// Sonarr: each dialog's ticks become classes on its library grid (prefix
// from data-prefix, opt- by default) and its size choice an attribute.
// Each browser remembers its choice.
(function () {
  document.querySelectorAll('dialog.poster-options').forEach(function (dialog) {
    var key = dialog.dataset.prefs;
    var grid = document.querySelector(dialog.dataset.grid || '.poster-grid[data-prefs="' + key + '"]');
    var form = dialog.querySelector('form');
    if (!grid || !form) return;
    var prefix = dialog.dataset.prefix || 'opt-', sizeAttr = dialog.dataset.sizeattr || 'size';
    var sizeSelect = form.querySelector('select[name="size"]');
    var boxes = form.querySelectorAll('input[type=checkbox]');
    var prefs = {};
    if (sizeSelect) prefs.size = sizeSelect.value;
    boxes.forEach(function (box) { prefs[box.name] = box.checked; });
    try {
      var saved = JSON.parse(localStorage.getItem(key) || 'null');
      if (saved) Object.keys(prefs).forEach(function (k) { if (k in saved) prefs[k] = saved[k]; });
    } catch (e) {}
    function apply() {
      if (sizeSelect) grid.dataset[sizeAttr] = prefs.size;
      Object.keys(prefs).forEach(function (k) {
        if (k !== 'size') grid.classList.toggle(prefix + k, !!prefs[k]);
      });
    }
    if (sizeSelect) sizeSelect.value = prefs.size;
    boxes.forEach(function (box) { box.checked = !!prefs[box.name]; });
    form.addEventListener('change', function () {
      if (sizeSelect) prefs.size = sizeSelect.value;
      boxes.forEach(function (box) { prefs[box.name] = box.checked; });
      try { localStorage.setItem(key, JSON.stringify(prefs)); } catch (e) {}
      apply();
    });
    apply();
  });
})();
