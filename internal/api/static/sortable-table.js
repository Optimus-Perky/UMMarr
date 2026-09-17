// Click-to-sort for any table.data-table th[data-sort] header - attached
// once at the document level (event delegation) rather than per-table, so
// it keeps working on tables htmx swaps in later (release_results.html's
// search results) without needing to re-run any setup code after a swap.
//
// A row followed by a tr.expand-row (the one-cell target an episode's
// search report lands in) keeps that row with it: the expand rows have no
// column to compare, so they're carried along rather than sorted.
document.addEventListener("click", function (event) {
  var th = event.target.closest("table.data-table th[data-sort]");
  if (!th) return;

  var table = th.closest("table");
  var headerRow = th.parentNode;
  var index = Array.prototype.indexOf.call(headerRow.children, th);
  var ascending = th.getAttribute("data-sort-dir") !== "asc";

  Array.prototype.forEach.call(headerRow.children, function (h) {
    h.removeAttribute("data-sort-dir");
  });
  th.setAttribute("data-sort-dir", ascending ? "asc" : "desc");

  var isNumber = th.getAttribute("data-sort") === "number";
  var tbody = table.querySelector("tbody");
  var rows = Array.prototype.slice.call(tbody.querySelectorAll(":scope > tr")).filter(function (row) {
    return !row.classList.contains("expand-row");
  });
  var expansions = rows.map(function (row) {
    var next = row.nextElementSibling;
    return next && next.classList.contains("expand-row") ? next : null;
  });
  var order = rows.map(function (row, i) { return {row: row, expand: expansions[i]}; });

  function value(row) {
    var cell = row.children[index];
    if (!cell) return "";
    return (cell.getAttribute("data-value") || cell.textContent).trim();
  }
  order.sort(function (a, b) {
    var valueA = value(a.row), valueB = value(b.row);
    if (isNumber) {
      valueA = parseFloat(valueA) || 0;
      valueB = parseFloat(valueB) || 0;
      return ascending ? valueA - valueB : valueB - valueA;
    }
    return ascending ? valueA.localeCompare(valueB) : valueB.localeCompare(valueA);
  });

  order.forEach(function (entry) {
    tbody.appendChild(entry.row);
    if (entry.expand) tbody.appendChild(entry.expand);
  });
});
