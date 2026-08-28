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

const saveStatus = document.querySelector('[data-entry-save-status]');
document.querySelectorAll('.entry-category-form, .entry-subcategory-form').forEach((form) => {
  const control = form.querySelector('select, input:not([type="hidden"])');
  if (!control) return;
  control.dataset.savedValue = control.value;

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const body = new FormData(form); // must snapshot before disabling, or the control drops out of the form data
    control.disabled = true;
    form.classList.add('saving');
    if (saveStatus) saveStatus.hidden = true;

    try {
      const response = await fetch(form.action, {
        method: 'POST',
        body,
        headers: {'X-Requested-With': 'fetch'},
      });
      if (!response.ok) throw new Error((await response.text()).trim() || 'Could not save the change.');
      control.dataset.savedValue = control.value;
    } catch (error) {
      control.value = control.dataset.savedValue;
      if (saveStatus) {
        saveStatus.textContent = error.message || 'Could not save the change.';
        saveStatus.hidden = false;
      }
    } finally {
      control.disabled = false;
      form.classList.remove('saving');
    }
  });
});