// The library toolbar, as in Radarr's movie index: sort, direction, built-in
// and custom filters (saved by name), a title search and an A-Z jump bar.
// Cards carry their keys as data attributes; everything happens in the page
// and each browser remembers its choice.
(function () {
  var FIELDS = {
    movies: [
      {key: 'title', label: 'Title', type: 'text'},
      {key: 'profile', label: 'Quality profile', type: 'text'},
      {key: 'path', label: 'Path', type: 'text'},
      {key: 'year', label: 'Year', type: 'number', scale: 1},
      {key: 'size', label: 'Size on disk (GiB)', type: 'number', scale: 1073741824},
      {key: 'tmdb', label: 'TMDb rating', type: 'number', scale: 1},
      {key: 'imdb', label: 'IMDb rating', type: 'number', scale: 1},
      {key: 'tomato', label: 'Tomato rating', type: 'number', scale: 1},
      {key: 'monitored', label: 'Monitored', type: 'bool'},
      {key: 'hasfile', label: 'Has file', type: 'bool'},
      {key: 'available', label: 'Available', type: 'bool'},
      {key: 'cutoffunmet', label: 'Cutoff unmet', type: 'bool'}
    ],
    tv: [
      {key: 'title', label: 'Title', type: 'text'},
      {key: 'profile', label: 'Quality profile', type: 'text'},
      {key: 'status', label: 'Status', type: 'text'},
      {key: 'path', label: 'Path', type: 'text'},
      {key: 'episodes', label: 'Episodes', type: 'number', scale: 1},
      {key: 'missing', label: 'Missing episodes', type: 'number', scale: 1},
      {key: 'monitored', label: 'Monitored', type: 'bool'},
      {key: 'cutoffunmet', label: 'Episodes below cutoff', type: 'number', scale: 1}
    ]
  };
  var BUILT_IN = {
    all: [],
    monitored: [{field: 'monitored', op: 'is', value: 'yes'}],
    unmonitored: [{field: 'monitored', op: 'is', value: 'no'}],
    missing: [{field: 'monitored', op: 'is', value: 'yes'}, {field: 'hasfile', op: 'is', value: 'no'}],
    'missing-episodes': [{field: 'monitored', op: 'is', value: 'yes'}, {field: 'missing', op: 'gt', value: '0'}],
    wanted: [{field: 'monitored', op: 'is', value: 'yes'}, {field: 'hasfile', op: 'is', value: 'no'}, {field: 'available', op: 'is', value: 'yes'}],
    downloaded: [{field: 'hasfile', op: 'is', value: 'yes'}],
    continuing: [{field: 'status', op: 'is', value: 'continuing'}],
    ended: [{field: 'status', op: 'is', value: 'ended'}],
    cutoff: [{field: 'cutoffunmet', op: 'is', value: 'yes'}],
    'cutoff-episodes': [{field: 'cutoffunmet', op: 'gt', value: '0'}]
  };
  var OPS = {
    text: [['contains', 'contains'], ['notcontains', 'does not contain'], ['is', 'is'], ['isnot', 'is not']],
    number: [['gt', 'greater than'], ['lt', 'less than'], ['eq', 'equal to']],
    bool: [['is', 'is']]
  };
  var NUMERIC = {added: 1, year: 1, cinema: 1, digital: 1, physical: 1, size: 1, tmdb: 1, imdb: 1, tomato: 1, episodes: 1, missing: 1, cutoffunmet: 1};

  function load(key, fallback) { try { var v = JSON.parse(localStorage.getItem(key) || 'null'); return v === null ? fallback : v; } catch (e) { return fallback; } }
  function save(key, value) { try { localStorage.setItem(key, JSON.stringify(value)); } catch (e) {} }
  function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;'); }
  function option(list, selected) {
    return list.map(function (p) { return '<option value="' + esc(p[0]) + '"' + (p[0] === selected ? ' selected' : '') + '>' + esc(p[1]) + '</option>'; }).join('');
  }

  function setup(grid) {
    var name = grid.dataset.library, fields = FIELDS[name] || [];
    var toolbar = document.querySelector('.library-toolbar[data-library="' + name + '"]');
    var slot = document.querySelector('.library-filter-slot[data-library="' + name + '"]');
    var letters = document.querySelector('.letter-bar[data-library="' + name + '"]');
    if (!toolbar) return;
    var sortSelect = toolbar.querySelector('.library-sort'), dirButton = toolbar.querySelector('.library-dir');
    var filterSelect = toolbar.querySelector('.library-filter'), search = toolbar.querySelector('.library-search'), shown = toolbar.querySelector('.library-shown');
    var count = document.querySelector('.section-count');
    var SORT = 'ummarr-' + name + '-sort', FILTER = 'ummarr-' + name + '-filter', SAVED = 'ummarr-' + name + '-filters';
    var sort = load(SORT, {key: 'sorttitle', dir: 'asc'}), filter = load(FILTER, 'all');
    var cards = Array.prototype.slice.call(grid.querySelectorAll('.card[data-sorttitle]'));
    var total = cards.length;

    function field(key) { for (var i = 0; i < fields.length; i++) if (fields[i].key === key) return fields[i]; return fields[0]; }
    function saved() { return load(SAVED, []); }
    function matches(card, c) {
      var f = field(c.field), raw = card.dataset[c.field] || '';
      if (f.type === 'number') {
        var have = parseFloat(raw) / f.scale, want = parseFloat(c.value);
        if (isNaN(want)) return true;
        return c.op === 'gt' ? have > want : c.op === 'lt' ? have < want : Math.abs(have - want) < 0.5;
      }
      if (f.type === 'bool') return (raw === '1') === (c.value === 'yes');
      var a = raw.toLowerCase(), b = (c.value || '').toLowerCase();
      switch (c.op) {
        case 'notcontains': return a.indexOf(b) < 0;
        case 'is': return a === b;
        case 'isnot': return a !== b;
        default: return a.indexOf(b) >= 0;
      }
    }
    function conditions() {
      if (filter.indexOf('saved:') === 0) {
        var f = saved().filter(function (x) { return x.name === filter.slice(6); })[0];
        return f ? f.conditions : [];
      }
      return BUILT_IN[filter] || [];
    }
    function compare(a, b) {
      var k = sort.key, x = a.dataset[k] || '', y = b.dataset[k] || '', r;
      if (NUMERIC[k]) {
        x = parseFloat(x) || 0; y = parseFloat(y) || 0;
        // Unknown dates and ratings sort last whichever way round.
        if (x === 0 && y !== 0) return 1;
        if (y === 0 && x !== 0) return -1;
        r = x - y;
      } else {
        r = x.localeCompare(y, undefined, {numeric: true, sensitivity: 'base'});
      }
      if (r === 0) r = a.dataset.sorttitle.localeCompare(b.dataset.sorttitle, undefined, {numeric: true, sensitivity: 'base'});
      return sort.dir === 'desc' ? -r : r;
    }
    function apply(custom) {
      var conds = custom || conditions(), q = (search.value || '').toLowerCase(), visible = 0, seen = {};
      cards.sort(compare);
      var frag = document.createDocumentFragment();
      cards.forEach(function (card) {
        var show = conds.every(function (c) { return matches(card, c); }) && (!q || card.dataset.title.toLowerCase().indexOf(q) >= 0);
        card.hidden = !show;
        if (show) { visible++; if (!seen[card.dataset.letter]) { seen[card.dataset.letter] = card; } }
        frag.appendChild(card);
      });
      grid.appendChild(frag);
      shown.textContent = visible === total ? '' : visible + ' of ' + total;
      if (count) count.textContent = visible === total ? total : visible + ' / ' + total;
      if (letters) {
        letters.hidden = sort.key !== 'sorttitle';
        letters.innerHTML = '#ABCDEFGHIJKLMNOPQRSTUVWXYZ'.split('').map(function (l) {
          return seen[l] ? '<button type="button" class="letter" data-letter="' + l + '">' + l + '</button>' : '<span class="letter letter-off">' + l + '</span>';
        }).join('');
      }
    }
    function refreshOptions() {
      filterSelect.querySelectorAll('option[data-custom]').forEach(function (o) { o.remove(); });
      var custom = filterSelect.querySelector('option[value="custom"]');
      saved().forEach(function (f) {
        var o = document.createElement('option');
        o.value = 'saved:' + f.name; o.textContent = f.name; o.dataset.custom = '1';
        filterSelect.insertBefore(o, custom);
      });
      filterSelect.value = filter;
      if (filterSelect.value !== filter) { filter = 'all'; filterSelect.value = 'all'; }
    }
    function conditionRow(c) {
      var f = field(c.field);
      var value = f.type === 'bool' ? '<select class="cf-value">' + option([['yes', 'yes'], ['no', 'no']], c.value) + '</select>'
        : '<input class="cf-value" type="' + (f.type === 'number' ? 'number' : 'text') + '" value="' + esc(c.value || '') + '">';
      return '<div class="cf-row"><select class="cf-field">' + option(fields.map(function (x) { return [x.key, x.label]; }), c.field) + '</select>' +
        '<select class="cf-op">' + option(OPS[f.type], c.op) + '</select>' + value +
        '<button type="button" class="icon-btn cf-remove" title="Remove condition">&times;</button></div>';
    }
    function readConditions(builder) {
      return Array.prototype.map.call(builder.querySelectorAll('.cf-row'), function (row) {
        return {field: row.querySelector('.cf-field').value, op: row.querySelector('.cf-op').value, value: row.querySelector('.cf-value').value};
      });
    }
    function openBuilder(existing) {
      var f = existing || {name: '', conditions: [{field: 'title', op: 'contains', value: ''}]};
      slot.innerHTML = '<div class="custom-filter">' +
        '<div class="cf-rows">' + f.conditions.map(conditionRow).join('') + '</div>' +
        '<div class="cf-actions"><button type="button" class="btn btn-sm btn-ghost cf-add">+ Condition</button>' +
        '<input class="cf-name" type="text" placeholder="Filter name (to save it)" value="' + esc(f.name) + '">' +
        '<button type="button" class="btn btn-sm btn-ghost cf-apply">Apply</button>' +
        '<button type="button" class="btn btn-sm cf-save">Save</button>' +
        (existing ? '<button type="button" class="btn btn-sm btn-danger cf-delete">Delete</button>' : '') +
        '<button type="button" class="btn btn-sm btn-ghost cf-close">Close</button></div></div>';
      slot.dataset.editing = existing ? existing.name : '';
    }

    sortSelect.value = sort.key;
    if (sortSelect.value !== sort.key) { sort.key = 'sorttitle'; sortSelect.value = sort.key; }
    dirButton.textContent = sort.dir === 'desc' ? '↓' : '↑';
    refreshOptions();
    sortSelect.addEventListener('change', function () { sort.key = sortSelect.value; save(SORT, sort); apply(); });
    dirButton.addEventListener('click', function () {
      sort.dir = sort.dir === 'desc' ? 'asc' : 'desc'; dirButton.textContent = sort.dir === 'desc' ? '↓' : '↑'; save(SORT, sort); apply();
    });
    search.addEventListener('input', function () { apply(); });
    filterSelect.addEventListener('change', function () {
      var v = filterSelect.value;
      if (v === 'custom') { openBuilder(null); return; }
      slot.innerHTML = ''; filter = v; save(FILTER, filter);
      if (v.indexOf('saved:') === 0) {
        var f = saved().filter(function (x) { return x.name === v.slice(6); })[0];
        if (f) openBuilder(f);
      }
      apply();
    });
    slot.addEventListener('click', function (e) {
      var builder = e.target.closest('.custom-filter');
      if (!builder) return;
      if (e.target.closest('.cf-add')) {
        builder.querySelector('.cf-rows').insertAdjacentHTML('beforeend', conditionRow({field: 'title', op: 'contains', value: ''}));
      } else if (e.target.closest('.cf-remove')) {
        e.target.closest('.cf-row').remove();
      } else if (e.target.closest('.cf-apply')) {
        apply(readConditions(builder));
      } else if (e.target.closest('.cf-save')) {
        var name = builder.querySelector('.cf-name').value.trim();
        if (!name) { builder.querySelector('.cf-name').focus(); return; }
        var list = saved().filter(function (f) { return f.name !== name && f.name !== slot.dataset.editing; });
        list.push({name: name, conditions: readConditions(builder)});
        save(SAVED, list); filter = 'saved:' + name; save(FILTER, filter); refreshOptions(); slot.innerHTML = ''; apply();
      } else if (e.target.closest('.cf-delete')) {
        save(SAVED, saved().filter(function (f) { return f.name !== slot.dataset.editing; }));
        filter = 'all'; save(FILTER, filter); refreshOptions(); slot.innerHTML = ''; apply();
      } else if (e.target.closest('.cf-close')) {
        slot.innerHTML = ''; filterSelect.value = filter;
      }
    });
    slot.addEventListener('change', function (e) {
      if (!e.target.closest('.cf-field')) return;
      var row = e.target.closest('.cf-row');
      row.outerHTML = conditionRow({field: e.target.value, op: OPS[field(e.target.value).type][0][0], value: ''});
    });
    if (letters) {
      letters.addEventListener('click', function (e) {
        var b = e.target.closest('button.letter');
        if (!b) return;
        var first = cards.filter(function (c) { return !c.hidden && c.dataset.letter === b.dataset.letter; })[0];
        if (first) first.scrollIntoView({behavior: 'smooth', block: 'start'});
      });
    }
    apply();
    grid.libraryGrid = {apply: apply, cards: cards};
  }
  document.querySelectorAll('.poster-grid[data-library]').forEach(setup);
})();
