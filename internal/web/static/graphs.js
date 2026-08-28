(function () {
  document.querySelectorAll('.chart-wrap').forEach((wrap) => {
    const tooltip = wrap.querySelector('.graph-tooltip');
    if (!tooltip) return;

    function show(point) {
      const wrapBox = wrap.getBoundingClientRect();
      const pointBox = point.getBoundingClientRect();
      tooltip.textContent = point.dataset.tooltip;
      tooltip.hidden = false;
      const halfWidth = tooltip.offsetWidth / 2;
      const center = pointBox.left - wrapBox.left + pointBox.width / 2;
      tooltip.style.left = `${Math.max(halfWidth + 4, Math.min(center, wrapBox.width - halfWidth - 4))}px`;
      tooltip.style.top = `${pointBox.top - wrapBox.top}px`;
    }

    function hide(event) {
      if (!event.relatedTarget?.closest?.('.graph-point')) tooltip.hidden = true;
    }

    wrap.addEventListener('pointerover', (event) => {
      const point = event.target.closest('.graph-point');
      if (point) show(point);
    });
    wrap.addEventListener('pointerout', hide);
    wrap.addEventListener('focusin', (event) => {
      const point = event.target.closest('.graph-point');
      if (point) show(point);
    });
    wrap.addEventListener('focusout', hide);
  });
})();