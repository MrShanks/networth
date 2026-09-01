// Widget board: reorder, collapse, and choose a standard width. The layout is
// remembered per browser, separately for each page.
(async function () {
  const pages = {
    overview: { label: 'Overview', path: '/' },
    portfolio: { label: 'Portfolio', path: '/portfolio' },
    expenses: { label: 'Expenses', path: '/expenses' },
    transactions: { label: 'Transactions', path: '/transactions' },
    graphs: { label: 'Graphs', path: '/graphs' },
    records: { label: 'Records', path: '/records' },
    retirement: { label: 'Retirement', path: '/retire' },
  };
  const pageOf = (path) => path.startsWith('/expenses') ? 'expenses'
    : path === '/' ? 'overview'
      : path === '/retire' ? 'retirement'
        : path.slice(1);
  const page = pageOf(location.pathname);
  const placementsKey = 'networth.widget.placements';
  const KEY = 'networth.board.' + location.pathname;
  const board = document.getElementById('board');
  if (!board) return;

  function loadPlacements() {
    try {
      return JSON.parse(localStorage.getItem(placementsKey)) || {};
    } catch (e) {
      return {};
    }
  }

  function widgetSettings(widget) {
    const title = widget.querySelector('[data-widget-title]');
    return {
      collapsed: widget.classList.contains('collapsed'),
      span: Number(widget.dataset.span) || 1,
      title: title?.textContent.trim() || title?.dataset.defaultTitle || '',
    };
  }

  const placements = loadPlacements();
  [...board.querySelectorAll('.widget')].forEach((widget) => {
    widget.dataset.widgetKey = `${page}::${widget.dataset.widget}`;
    widget.dataset.widgetHome = page;
    widget.dataset.widgetSource = location.pathname;
    const placement = placements[widget.dataset.widgetKey];
    if (placement && placement.destination !== page) widget.remove();
  });

  const sourceDocuments = [];
  const incoming = Object.entries(placements)
    .filter(([, placement]) => placement.destination === page);
  for (const [key, placement] of incoming) {
    if (board.querySelector(`[data-widget-key="${CSS.escape(key)}"]`)) continue;
    try {
      const response = await fetch(placement.source, { headers: { Accept: 'text/html' } });
      if (!response.ok) continue;
      const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
      const original = [...doc.querySelectorAll('#board > .widget')]
        .find((widget) => widget.dataset.widget === placement.widget);
      if (!original) continue;
      const widget = document.importNode(original, true);
      widget.dataset.widgetKey = key;
      widget.dataset.widgetHome = placement.home;
      widget.dataset.widgetSource = placement.source;
      if (placement.settings?.collapsed) widget.classList.add('collapsed');
      if ([1, 2, 4].includes(placement.settings?.span)) {
        widget.dataset.span = placement.settings.span;
      }
      const title = widget.querySelector('[data-widget-title]');
      if (title && placement.settings?.title) title.textContent = placement.settings.title;
      board.appendChild(widget);
      sourceDocuments.push(doc);
    } catch (e) {
      // Leave an unavailable widget at home rather than breaking this page.
    }
  }

  async function activateSourceScripts(doc) {
    for (const script of doc.querySelectorAll('body script')) {
      if (script.src) {
        const src = new URL(script.src, location.origin).pathname;
        if (src.endsWith('/board.js') || src.endsWith('/scroll.js')
            || document.querySelector(`script[src="${CSS.escape(src)}"]`)) continue;
        await new Promise((resolve) => {
          const loaded = document.createElement('script');
          loaded.src = src;
          loaded.onload = resolve;
          loaded.onerror = resolve;
          document.body.appendChild(loaded);
        });
      } else if (script.textContent.trim()) {
        Function(script.textContent)();
      }
    }
  }
  for (const doc of sourceDocuments) await activateSourceScripts(doc);

  const widgets = () => [...board.querySelectorAll('.widget')];
  const idOf = (el) => el.dataset.widgetKey || el.dataset.widget;

  function load() {
    try {
      const state = JSON.parse(localStorage.getItem(KEY)) || {};
      if (state.widgetKeys) return state;
      const localIDs = new Set([...board.querySelectorAll('[data-widget-home]')]
        .filter((widget) => widget.dataset.widgetHome === page)
        .map((widget) => widget.dataset.widget));
      const key = (id) => localIDs.has(id) ? `${page}::${id}` : id;
      state.order = (state.order || []).map(key);
      state.collapsed = (state.collapsed || []).map(key);
      for (const property of ['spans', 'columns', 'titles']) {
        state[property] = Object.fromEntries(Object.entries(state[property] || {})
          .map(([id, value]) => [key(id), value]));
      }
      state.widgetKeys = true;
      localStorage.setItem(KEY, JSON.stringify(state));
      return state;
    } catch (e) {
      return {};
    }
  }

  // The saved order also holds widgets the page isn't showing right now (a
  // chart with no data yet, say), so they come back where they were left.
  let remembered = [];

  function mergedOrder() {
    const present = new Set(widgets().map(idOf));
    const order = widgets().map(idOf);
    let anchor = null;
    for (const id of remembered) {
      if (present.has(id)) {
        anchor = id;
        continue;
      }
      order.splice(anchor === null ? 0 : order.indexOf(anchor) + 1, 0, id);
      anchor = id;
    }
    return order;
  }

  // keptFor carries over what was saved for widgets this page is not showing.
  function keptFor(previous, current, present) {
    const merged = { ...current };
    Object.entries(previous || {}).forEach(([id, value]) => {
      if (!present.has(id)) merged[id] = value;
    });
    return merged;
  }

  function save() {
    const previous = load();
    const present = new Set(widgets().map(idOf));
    remembered = mergedOrder();
    const state = {
      order: remembered,
      collapsed: [...(previous.collapsed || []).filter((id) => !present.has(id)),
        ...widgets().filter((w) => w.classList.contains('collapsed')).map(idOf)],
      spans: keptFor(previous.spans, Object.fromEntries(
        widgets().map((w) => [idOf(w), Number(w.dataset.span) || 1])), present),
      columns: keptFor(previous.columns, Object.fromEntries(
        widgets().filter((w) => w.dataset.column).map((w) => [idOf(w), Number(w.dataset.column)])), present),
      titles: keptFor(previous.titles, Object.fromEntries(widgets().map((w) => {
        const title = w.querySelector('[data-widget-title]');
        return [idOf(w), title?.textContent.trim() || title?.dataset.defaultTitle || ''];
      })), present),
      widgetKeys: true,
    };
    localStorage.setItem(KEY, JSON.stringify(state));
  }

  function restore() {
    const state = load();
    const byID = new Map(widgets().map((w) => [idOf(w), w]));

    remembered = (state.order || []).map(String);
    const known = new Set(remembered);
    // A widget the saved order never saw is a new one: it goes at the end.
    const fresh = widgets().map(idOf).filter((id) => !known.has(id));
    remembered = remembered.concat(fresh);
    remembered.forEach((id) => {
      const widget = byID.get(id);
      if (widget) board.appendChild(widget);
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

    if (fresh.length || !state.order) save(); // pin the places down from now on
  }

  widgets().forEach((widget) => {
    const title = widget.querySelector('[data-widget-title]');
    if (title) title.dataset.defaultTitle = title.textContent.trim();

    const tools = widget.querySelector('.widget-tools');
    if (!tools || tools.querySelector('.widget-move')) return;
    const select = document.createElement('select');
    select.className = 'widget-move';
    select.setAttribute('aria-label', `Move ${title?.textContent.trim() || 'widget'} to another page`);
    select.title = 'Move to another page';
    select.innerHTML = '<option value="">Move to...</option>'
      + Object.entries(pages)
        .filter(([id]) => id !== page)
        .map(([id, destination]) => `<option value="${id}">${destination.label}</option>`)
        .join('');
    tools.insertBefore(select, tools.firstChild);
    select.addEventListener('change', () => {
      const destination = select.value;
      if (!destination) return;
      const key = idOf(widget);
      const home = widget.dataset.widgetHome;
      if (destination === home) {
        delete placements[key];
      } else {
        placements[key] = {
          home,
          source: widget.dataset.widgetSource,
          widget: widget.dataset.widget,
          destination,
          settings: widgetSettings(widget),
        };
      }
      localStorage.setItem(placementsKey, JSON.stringify(placements));
      save();
      location.href = pages[destination].path;
    });
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
    const gap = parseFloat(styles.getPropertyValue('--widget-row-gap')) || 0;
    if (!row) return;
    widgets().forEach((widget) => {
      const height = widget.getBoundingClientRect().height;
      widget.style.gridRowEnd = `span ${Math.ceil((height + gap) / row)}`;
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

  board.addEventListener('toggle', (e) => {
    if (e.target.matches('details')) layout();
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
