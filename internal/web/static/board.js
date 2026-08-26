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
      columns: Object.fromEntries(widgets().filter((w) => w.dataset.column)
        .map((w) => [idOf(w), Number(w.dataset.column)])),
      titles: Object.fromEntries(widgets().map((w) => {
        const title = w.querySelector('[data-widget-title]');
        return [idOf(w), title?.textContent.trim() || title?.dataset.defaultTitle || ''];
      })),
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
    Object.entries(state.columns || {}).forEach(([id, column]) => {
      if (byID.has(id)) byID.get(id).dataset.column = column;
    });
    Object.entries(state.titles || {}).forEach(([id, value]) => {
      const title = byID.get(id)?.querySelector('[data-widget-title]');
      if (title && String(value).trim()) title.textContent = String(value).trim();
    });
  }

  widgets().forEach((widget) => {
    const title = widget.querySelector('[data-widget-title]');
    if (title) title.dataset.defaultTitle = title.textContent.trim();
  });

  function columnCount() {
    if (matchMedia('(max-width: 720px)').matches) return 1;
    if (matchMedia('(max-width: 1100px)').matches) return 2;
    return 4;
  }

  function effectiveSpan(widget, columns) {
    const span = Number(widget.dataset.span) || 1;
    return Math.min(span === 4 && columns === 2 ? 2 : span, columns);
  }

  function placeColumns() {
    const columns = columnCount();
    widgets().forEach((widget) => {
      const saved = Number(widget.dataset.column);
      const span = effectiveSpan(widget, columns);
      widget.style.gridColumn = saved
        ? `${Math.min(saved, columns - span + 1)} / span ${span}`
        : '';
    });
  }

  function sizeRows() {
    const styles = getComputedStyle(board);
    const row = parseFloat(styles.gridAutoRows);
    const gap = parseFloat(styles.columnGap);
    if (!row) return;
    widgets().forEach((widget) => {
      widget.style.gridRowEnd = `span ${Math.ceil((widget.getBoundingClientRect().height + gap) / row)}`;
    });
  }

  function fitCategoryTags() {
    board.querySelectorAll('.entry-category').forEach((tag) => {
      tag.style.fontSize = '';
      tag.style.whiteSpace = 'nowrap';
      const available = tag.parentElement.clientWidth
        - parseFloat(getComputedStyle(tag.parentElement).paddingLeft)
        - parseFloat(getComputedStyle(tag.parentElement).paddingRight);
      let size = parseFloat(getComputedStyle(tag).fontSize);
      while (tag.scrollWidth > available && size > 9) {
        size -= 0.5;
        tag.style.fontSize = `${size}px`;
      }
      if (tag.scrollWidth > available) tag.style.whiteSpace = 'normal';
    });
  }

  function layout(alignFragment = false) {
    placeColumns();
    requestAnimationFrame(() => {
      fitCategoryTags();
      sizeRows();
      if (alignFragment && location.hash) {
        document.querySelector(location.hash)?.scrollIntoView();
      }
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
    layout();
    save();
  });

  // Collapsing.
  board.addEventListener('click', (e) => {
    const button = e.target.closest('[data-collapse]');
    if (!button) return;
    button.closest('.widget').classList.toggle('collapsed');
    layout();
    save();
  });

  // Inline titles: Enter commits, Escape restores the previous value, and a
  // blank title resets to the server-provided default.
  let titleBeforeEdit = '';
  board.addEventListener('focusin', (e) => {
    const title = e.target.closest('[data-widget-title]');
    if (title) titleBeforeEdit = title.textContent.trim();
  });
  board.addEventListener('keydown', (e) => {
    const title = e.target.closest('[data-widget-title]');
    if (!title) return;
    if (e.key === 'Enter') {
      e.preventDefault();
      title.blur();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      title.textContent = titleBeforeEdit || title.dataset.defaultTitle;
      title.blur();
    }
  });
  board.addEventListener('blur', (e) => {
    const title = e.target.closest('[data-widget-title]');
    if (!title) return;
    title.textContent = title.textContent.trim() || title.dataset.defaultTitle;
    save();
    layout();
  }, true);

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

    const boardBox = board.getBoundingClientRect();
    const columns = columnCount();
    const span = effectiveSpan(dragged, columns);
    const pointed = Math.floor((e.clientX - boardBox.left) / (boardBox.width / columns)) + 1;
    dragged.dataset.column = Math.max(1, Math.min(pointed, columns - span + 1));
    placeColumns();

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
    layout();
    save();
  });

  board.addEventListener('drop', (e) => e.preventDefault());

  const accountsBody = board.querySelector('.accounts-table > tbody');
  if (accountsBody) {
    const orderKey = 'networth.accounts.order';
    const accountRows = () => [...accountsBody.querySelectorAll(':scope > .account-row')];
    const fundRow = (row) => row.nextElementSibling?.classList.contains('account-funds')
      ? row.nextElementSibling
      : null;
    const moveGroupBefore = (row, before) => {
      const funds = fundRow(row);
      if (before === row || before === funds) return;
      accountsBody.insertBefore(row, before);
      if (funds) accountsBody.insertBefore(funds, before);
    };

    try {
      const byID = new Map(accountRows().map((row) => [row.dataset.account, row]));
      const saved = JSON.parse(localStorage.getItem(orderKey) || '[]').map(String);
      const savedIDs = new Set(saved);
      const ordered = saved.map((id) => byID.get(id)).filter(Boolean)
        .concat(accountRows().filter((row) => !savedIDs.has(row.dataset.account)));
      ordered.forEach((row) => moveGroupBefore(row, null));
    } catch (e) {
      // Ignore invalid saved layout and use the server order.
    }

    let draggedAccount = null;
    accountsBody.addEventListener('dragstart', (e) => {
      if (!e.target.closest('.account-grip')) return;
      draggedAccount = e.target.closest('.account-row');
      draggedAccount.classList.add('dragging');
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', draggedAccount.dataset.account);
    });
    accountsBody.addEventListener('dragover', (e) => {
      if (!draggedAccount) return;
      const target = e.target.closest('.account-row');
      if (!target || target === draggedAccount) return;
      e.preventDefault();
      const before = e.clientY < target.getBoundingClientRect().top + target.offsetHeight / 2;
      moveGroupBefore(draggedAccount, before ? target : fundRow(target)?.nextElementSibling || target.nextElementSibling);
    });
    accountsBody.addEventListener('dragend', () => {
      if (!draggedAccount) return;
      draggedAccount.classList.remove('dragging');
      draggedAccount = null;
      localStorage.setItem(orderKey, JSON.stringify(accountRows().map((row) => row.dataset.account)));
      layout();
    });
  }

  restore();
  layout(true);
  new ResizeObserver(sizeRows).observe(board);
  window.addEventListener('resize', layout);
})();
