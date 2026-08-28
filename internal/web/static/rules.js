(function () {
  const form = document.getElementById('rule-form');
  if (!form) return;
  const show = document.getElementById('show-rule-form');
  const mode = document.getElementById('rule-mode');
  const pattern = document.getElementById('rule-pattern');
  const label = document.getElementById('rule-pattern-label');
  const submit = document.getElementById('rule-submit');
  const cancel = document.getElementById('cancel-rule-edit');
  const updateHelp = () => { label.textContent = mode.value === 'regex' ? 'Regular expression' : 'Terms, one per line'; };
  const reset = () => { form.reset(); form.action = '/rules'; submit.textContent = 'Save rule'; form.hidden = true; show.setAttribute('aria-expanded', 'false'); updateHelp(); };
  show.addEventListener('click', () => { form.hidden = false; show.setAttribute('aria-expanded', 'true'); pattern.focus(); dispatchEvent(new Event('resize')); });
  mode.addEventListener('change', updateHelp);
  cancel.addEventListener('click', reset);
  document.querySelector('.rules-table tbody')?.addEventListener('click', (event) => {
    const edit = event.target.closest('.edit-rule');
    if (!edit) return;
    form.hidden = false; show.setAttribute('aria-expanded', 'true'); form.action = `/rules/${edit.dataset.ruleId}`;
    mode.value = edit.dataset.ruleMode; pattern.value = edit.dataset.rulePattern;
    form.querySelector('[name="new_category"]').value = edit.dataset.ruleCategory;
    form.querySelector('[name="subcategory"]').value = edit.dataset.ruleSubcategory;
    submit.textContent = 'Update rule'; pattern.focus(); dispatchEvent(new Event('resize'));
  });
})();