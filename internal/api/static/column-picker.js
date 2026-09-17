// Per-table column show/hide, remembered per browser via localStorage -
// event delegation (like sortable-table.js), so it keeps working on
// tables htmx swaps in later with zero setup code, and a shared
// applyStoredColumns() re-applies saved state after every such swap.

function storageKey(tableId) {
  return "ummarr-columns-" + tableId;
}

function hiddenColumns(tableId) {
  var saved = null;
  try {
    saved = localStorage.getItem(storageKey(tableId));
  } catch (e) {}
  if (saved !== null) {
    try {
      return JSON.parse(saved);
    } catch (e) {}
  }
  // Nothing saved yet: a table can name the columns that start hidden.
  var table = document.querySelector('table[data-table-id="' + tableId + '"][data-default-hidden]');
  return table ? table.getAttribute("data-default-hidden").split(",") : [];
}

function eachOf(root, selector, fn) {
  Array.prototype.forEach.call(root.querySelectorAll(selector), fn);
}

// applyStoredColumns is authoritative: it sets the shown/hidden state of
// every column it finds, rather than only hiding the saved ones, so it
// can be re-run after any change to bring every table and every picker
// back in sync with localStorage.
function applyStoredColumns() {
  eachOf(document, "table[data-table-id]", function (table) {
    var hidden = hiddenColumns(table.getAttribute("data-table-id"));
    // Cells only - a picker's own checkboxes carry data-col too and now
    // live inside the table, in the header's gear menu.
    eachOf(table, "th[data-col],td[data-col]", function (cell) {
      cell.hidden = hidden.indexOf(cell.getAttribute("data-col")) !== -1;
    });
  });
  // One table id can own several pickers (each season renders its own
  // gear), and they all show the same saved state.
  eachOf(document, ".col-picker[data-for]", function (picker) {
    var hidden = hiddenColumns(picker.getAttribute("data-for"));
    eachOf(picker, "input[type=checkbox]", function (input) {
      input.checked = hidden.indexOf(input.getAttribute("data-col")) === -1;
    });
  });
}

function closeAllColumnPickers() {
  eachOf(document, ".col-picker-panel", function (panel) {
    panel.hidden = true;
  });
}

document.addEventListener("click", function (event) {
  var button = event.target.closest(".col-picker > button");
  if (button) {
    var panel = button.nextElementSibling;
    var wasHidden = panel && panel.hidden;
    // Close every other open picker panel first, so at most one floats
    // over the page at a time.
    closeAllColumnPickers();
    if (panel) panel.hidden = !wasHidden;
    return;
  }
  // Click anywhere outside a panel closes it; clicks inside keep it open
  // so several columns can be toggled in one go, like Sonarr's.
  if (!event.target.closest(".col-picker-panel")) {
    closeAllColumnPickers();
  }
});

document.addEventListener("change", function (event) {
  var input = event.target.closest(".col-picker-panel input[type=checkbox]");
  if (!input) return;
  var tableId = input.closest(".col-picker").getAttribute("data-for");
  var col = input.getAttribute("data-col");

  var hidden = hiddenColumns(tableId).filter(function (c) {
    return c !== col;
  });
  if (!input.checked) hidden.push(col);
  localStorage.setItem(storageKey(tableId), JSON.stringify(hidden));
  applyStoredColumns();
});

document.addEventListener("DOMContentLoaded", applyStoredColumns);
document.addEventListener("htmx:afterSwap", applyStoredColumns);
