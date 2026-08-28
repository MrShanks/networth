document.querySelectorAll('[data-sortable-entries]').forEach((table) => {
  const body = table.tBodies[0];
  [...table.querySelectorAll('[data-sort-column]')].forEach((button, columnIndex) => {
    button.addEventListener('click', () => {
      const heading = button.closest('th');
      const ascending = heading.getAttribute('aria-sort') !== 'ascending';
      table.querySelectorAll('th[aria-sort]').forEach((th) => th.setAttribute('aria-sort', 'none'));
      heading.setAttribute('aria-sort', ascending ? 'ascending' : 'descending');
      const numeric = button.dataset.sortType === 'number';
      const rows = [...body.rows];
      rows.sort((left, right) => {
        const leftValue = left.cells[columnIndex].dataset.sortValue || '';
        const rightValue = right.cells[columnIndex].dataset.sortValue || '';
        const compared = numeric
          ? Number(leftValue.replaceAll(',', '')) - Number(rightValue.replaceAll(',', ''))
          : leftValue.localeCompare(rightValue, undefined, { sensitivity: 'base' });
        return (ascending ? compared : -compared) || Number(left.dataset.entryId) - Number(right.dataset.entryId);
      });
      body.append(...rows);
    });
  });
});