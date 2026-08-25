// Widget board: reorder, collapse, and choose a standard width. The layout is
// remembered per browser, separately for each page.
(function () {
  const KEY = 'networth.board.' + location.pathname;
  const board = document.getElementById('board');
  if (!board) return;

  const widgets = () => [...board.querySelectorAll('.widget')];
  const idOf = (el) => el.dataset.widget;

  function load() {
    try {
      return JSON.parse(localStorage.getItem(KEY)) || {};
    } catch (e) {
      return {};
    }
  }

  function save() {
    const state = {
      order: widgets().map(idOf),
      collapsed: widgets().filter((w) => w.classList.contains('collapsed')).map(idOf),
      spans: Object.fromEntries(widgets().map((w) => [idOf(w), Number(w.dataset.span) || 1])),
    };
    localStorage.setItem(KEY, JSON.stringify(state));
  }

  function restore() {
    const state = load();
    const byID = new Map(widgets().map((w) => [idOf(w), w]));

    (state.order || []).forEach((id) => {
      const widget = byID.get(id);
      if (widget) board.appendChild(widget); // known ones first, in saved order
    });
    (state.collapsed || []).forEach((id) => byID.get(id)?.classList.add('collapsed'));
    Object.entries(state.spans || {}).forEach(([id, span]) => {
      if (byID.has(id) && [1, 2, 4].includes(span)) byID.get(id).dataset.span = span;
    });
  }

  // Widths snap to the board's standard column spans.
  board.addEventListener('click', (e) => {
    const button = e.target.closest('[data-resize]');
    if (!button) return;
    const widget = button.closest('.widget');
    const current = Number(widget.dataset.span) || 1;
    widget.dataset.span = current === 1 ? 2 : current === 2 ? 4 : 1;
    button.setAttribute('aria-label', `Change width; currently ${widget.dataset.span} columns`);
    save();
  });

  // Collapsing.
  board.addEventListener('click', (e) => {
    const button = e.target.closest('[data-collapse]');
    if (!button) return;
    button.closest('.widget').classList.toggle('collapsed');
    save();
  });

  // Dragging: the grip carries the drag, the widget is what moves.
  let dragged = null;

  board.addEventListener('dragstart', (e) => {
    const grip = e.target.closest('.grip');
    if (!grip) return;
    dragged = grip.closest('.widget');
    dragged.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', idOf(dragged));
    e.dataTransfer.setDragImage(dragged, 20, 20);
  });

  board.addEventListener('dragover', (e) => {
    if (!dragged) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';

    const target = e.target.closest('.widget');
    if (!target || target === dragged) return;

    // Snap before or after the widget under the cursor, whichever edge is nearer.
    const box = target.getBoundingClientRect();
    const after = sameRow(box, dragged.getBoundingClientRect())
      ? e.clientX > box.left + box.width / 2
      : e.clientY > box.top + box.height / 2;
    board.insertBefore(dragged, after ? target.nextSibling : target);
  });

  function sameRow(a, b) {
    return Math.abs(a.top - b.top) < Math.min(a.height, b.height) / 2;
  }

  board.addEventListener('dragend', () => {
    if (!dragged) return;
    dragged.classList.remove('dragging');
    dragged = null;
    save();
  });

  board.addEventListener('drop', (e) => e.preventDefault());

  document.getElementById('reset-layout')?.addEventListener('click', () => {
    localStorage.removeItem(KEY);
    location.reload();
  });

  restore();
})();
