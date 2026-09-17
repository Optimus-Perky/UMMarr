// The calendar's Movies / TV / Music / Unmonitored toggles. Each browser
// remembers what it wants to see.
(function () {
  var KEY = 'ummarr-calendar-kinds';
  var calendar = document.getElementById('calendar');
  var toggles = document.querySelectorAll('.calendar-toggle');
  if (!calendar || !toggles.length) return;
  var prefs = {};
  toggles.forEach(function (box) { prefs[box.dataset.kind] = box.checked; });
  try {
    var saved = JSON.parse(localStorage.getItem(KEY) || 'null');
    if (saved) Object.keys(prefs).forEach(function (k) { if (k in saved) prefs[k] = saved[k]; });
  } catch (e) {}
  function apply() {
    toggles.forEach(function (box) { box.checked = !!prefs[box.dataset.kind]; });
    Array.prototype.forEach.call(calendar.querySelectorAll('.calendar-entry'), function (entry) {
      var kindOff = !prefs[entry.dataset.kind];
      var eventOff = entry.dataset.event && !prefs[entry.dataset.event];
      var unmonitoredHidden = entry.dataset.monitored === '0' && !prefs.unmonitored;
      entry.hidden = kindOff || eventOff || unmonitoredHidden;
    });
  }
  toggles.forEach(function (box) {
    box.addEventListener('change', function () {
      prefs[box.dataset.kind] = box.checked;
      try { localStorage.setItem(KEY, JSON.stringify(prefs)); } catch (e) {}
      apply();
    });
  });
  apply();
})();
