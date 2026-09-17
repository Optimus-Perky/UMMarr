// The interactive search dialog: opens when results land in it, has
// Details / History / Search tabs, and filters the result rows - with
// Sonarr-style custom filters (column, operator, value; any number of
// conditions; saved by name per browser).
(function () {
  var STORAGE = 'ummarr-release-filters';
  var FIELDS = [
    {key: 'title', label: 'Title', type: 'text'},
    {key: 'indexer', label: 'Indexer', type: 'text'},
    {key: 'quality', label: 'Quality', type: 'text'},
    {key: 'group', label: 'Release group', type: 'text'},
    {key: 'languages', label: 'Languages', type: 'text'},
    {key: 'flags', label: 'Flags', type: 'text'},
    {key: 'size', label: 'Size (MiB)', type: 'number', scale: 1048576},
    {key: 'seeders', label: 'Seeders', type: 'number', scale: 1},
    {key: 'age', label: 'Age (days)', type: 'number', scale: 1440},
    {key: 'approved', label: 'Approved', type: 'bool'},
    {key: 'seasonpack', label: 'Season pack', type: 'bool'},
    {key: 'protocol', label: 'Protocol', type: 'protocol'}
  ];
  var OPS = {
    text: [['contains', 'contains'], ['notcontains', 'does not contain'], ['is', 'is'], ['isnot', 'is not']],
    number: [['gt', 'greater than'], ['lt', 'less than'], ['eq', 'equal to']],
    bool: [['is', 'is']],
    protocol: [['is', 'is']]
  };
  function field(key) { for (var i = 0; i < FIELDS.length; i++) if (FIELDS[i].key === key) return FIELDS[i]; return FIELDS[0]; }
  function saved() { try { return JSON.parse(localStorage.getItem(STORAGE) || '[]'); } catch (e) { return []; } }
  function store(list) { try { localStorage.setItem(STORAGE, JSON.stringify(list)); } catch (e) {} }

  function matches(row, c) {
    var f = field(c.field), raw = row.dataset[c.field] || '';
    if (f.type === 'number') {
      var have = parseFloat(raw) / f.scale, want = parseFloat(c.value);
      if (isNaN(want)) return true;
      return c.op === 'gt' ? have > want : c.op === 'lt' ? have < want : Math.abs(have - want) < 0.5;
    }
    if (f.type === 'bool') return (raw === '1') === (c.value === 'yes');
    if (f.type === 'protocol') return raw === c.value;
    var a = raw.toLowerCase(), b = (c.value || '').toLowerCase();
    switch (c.op) {
      case 'notcontains': return a.indexOf(b) < 0;
      case 'is': return a === b;
      case 'isnot': return a !== b;
      default: return a.indexOf(b) >= 0;
    }
  }
  function apply(pane, conditions) {
    var table = pane.querySelector('table.data-table');
    if (!table) return;
    table.querySelectorAll('tbody tr[data-approved]').forEach(function (row) {
      row.hidden = !conditions.every(function (c) { return matches(row, c); });
    });
  }
  var BUILT_IN = {
    all: [], approved: [{field: 'approved', op: 'is', value: 'yes'}], rejected: [{field: 'approved', op: 'is', value: 'no'}],
    torrent: [{field: 'protocol', op: 'is', value: 'torrent'}], usenet: [{field: 'protocol', op: 'is', value: 'usenet'}],
    'season-pack': [{field: 'seasonpack', op: 'is', value: 'yes'}], 'not-season-pack': [{field: 'seasonpack', op: 'is', value: 'no'}]
  };

  function refreshOptions(select) {
    select.querySelectorAll('option[data-custom]').forEach(function (o) { o.remove(); });
    var custom = select.querySelector('option[value="custom"]');
    saved().forEach(function (f) {
      var o = document.createElement('option');
      o.value = 'saved:' + f.name; o.textContent = f.name; o.dataset.custom = '1';
      select.insertBefore(o, custom);
    });
  }
  function option(list, selected) {
    return list.map(function (p) { return '<option value="' + p[0] + '"' + (p[0] === selected ? ' selected' : '') + '>' + p[1] + '</option>'; }).join('');
  }
  function conditionRow(c) {
    var f = field(c.field);
    var value = f.type === 'bool' ? '<select class="cf-value">' + option([['yes', 'yes'], ['no', 'no']], c.value) + '</select>'
      : f.type === 'protocol' ? '<select class="cf-value">' + option([['torrent', 'torrent'], ['usenet', 'usenet']], c.value) + '</select>'
      : '<input class="cf-value" type="' + (f.type === 'number' ? 'number' : 'text') + '" value="' + (c.value || '').replace(/"/g, '&quot;') + '">';
    return '<div class="cf-row"><select class="cf-field">' + option(FIELDS.map(function (x) { return [x.key, x.label]; }), c.field) + '</select>' +
      '<select class="cf-op">' + option(OPS[f.type], c.op) + '</select>' + value +
      '<button type="button" class="icon-btn cf-remove" title="Remove condition">&times;</button></div>';
  }
  function readConditions(builder) {
    return Array.prototype.map.call(builder.querySelectorAll('.cf-row'), function (row) {
      return {field: row.querySelector('.cf-field').value, op: row.querySelector('.cf-op').value, value: row.querySelector('.cf-value').value};
    });
  }
  function openBuilder(pane, select, existing) {
    var slot = pane.querySelector('.custom-filter-slot');
    var f = existing || {name: '', conditions: [{field: 'title', op: 'contains', value: ''}]};
    slot.innerHTML = '<div class="custom-filter">' +
      '<div class="cf-rows">' + f.conditions.map(conditionRow).join('') + '</div>' +
      '<div class="cf-actions"><button type="button" class="btn btn-sm btn-ghost cf-add">+ Condition</button>' +
      '<input class="cf-name" type="text" placeholder="Filter name (to save it)" value="' + f.name.replace(/"/g, '&quot;') + '">' +
      '<button type="button" class="btn btn-sm btn-ghost cf-apply">Apply</button>' +
      '<button type="button" class="btn btn-sm cf-save">Save</button>' +
      (existing ? '<button type="button" class="btn btn-sm btn-danger cf-delete">Delete</button>' : '') +
      '<button type="button" class="btn btn-sm btn-ghost cf-close">Close</button></div></div>';
    slot.dataset.editing = existing ? existing.name : '';
  }

  document.body.addEventListener('htmx:afterSwap', function (e) {
    if (!e.detail.target || e.detail.target.id !== 'release-dialog-body') return;
    var dialog = document.getElementById('release-dialog');
    if (dialog && !dialog.open) dialog.showModal();
    dialog.scrollTop = 0;
    var select = e.detail.target.querySelector('.release-filter');
    if (select) { refreshOptions(select); apply(select.closest('.dialog-pane'), BUILT_IN[select.value] || []); }
  });
  document.body.addEventListener('click', function (e) {
    var tab = e.target.closest('.dialog-tab');
    if (tab) {
      var root = tab.closest('#release-dialog-body');
      root.querySelectorAll('.dialog-tab').forEach(function (t) { t.classList.toggle('active', t === tab); });
      root.querySelectorAll('.dialog-pane').forEach(function (p) { p.hidden = p.dataset.pane !== tab.dataset.tab; });
      return;
    }
    var builder = e.target.closest('.custom-filter');
    if (!builder) return;
    var pane = builder.closest('.dialog-pane'), select = pane.querySelector('.release-filter'), slot = pane.querySelector('.custom-filter-slot');
    if (e.target.closest('.cf-add')) {
      builder.querySelector('.cf-rows').insertAdjacentHTML('beforeend', conditionRow({field: 'title', op: 'contains', value: ''}));
    } else if (e.target.closest('.cf-remove')) {
      e.target.closest('.cf-row').remove();
    } else if (e.target.closest('.cf-apply')) {
      apply(pane, readConditions(builder));
    } else if (e.target.closest('.cf-save')) {
      var name = builder.querySelector('.cf-name').value.trim();
      if (!name) { builder.querySelector('.cf-name').focus(); return; }
      var list = saved().filter(function (f) { return f.name !== name && f.name !== slot.dataset.editing; });
      list.push({name: name, conditions: readConditions(builder)});
      store(list); refreshOptions(select); select.value = 'saved:' + name;
      apply(pane, readConditions(builder)); slot.innerHTML = '';
    } else if (e.target.closest('.cf-delete')) {
      store(saved().filter(function (f) { return f.name !== slot.dataset.editing; }));
      refreshOptions(select); select.value = 'all'; apply(pane, []); slot.innerHTML = '';
    } else if (e.target.closest('.cf-close')) {
      slot.innerHTML = ''; if (select.value === 'custom') select.value = 'all';
    }
  });
  document.body.addEventListener('change', function (e) {
    var target = e.target;
    if (target.closest('.cf-field')) {
      // A different column changes which operators and value box make sense.
      var row = target.closest('.cf-row');
      row.outerHTML = conditionRow({field: target.value, op: OPS[field(target.value).type][0][0], value: ''});
      return;
    }
    var select = target.closest('.release-filter');
    if (!select) return;
    var pane = select.closest('.dialog-pane');
    if (select.value === 'custom') { openBuilder(pane, select, null); return; }
    pane.querySelector('.custom-filter-slot').innerHTML = '';
    if (select.value.indexOf('saved:') === 0) {
      var name = select.value.slice(6), f = saved().filter(function (x) { return x.name === name; })[0];
      if (f) { apply(pane, f.conditions); openBuilder(pane, select, f); }
      return;
    }
    apply(pane, BUILT_IN[select.value] || []);
  });
  // For tests and the console.
  window.ummarrReleaseFilter = {apply: apply, fields: FIELDS};
})();
