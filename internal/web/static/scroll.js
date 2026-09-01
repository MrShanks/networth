// Keeps the page where it was when a form is submitted: the redirect that
// follows is a fresh page load, which would otherwise jump back to the top.
(function () {
  const key = 'networth.scroll.' + location.pathname;
  const fresh = 10000; // ms; an older mark belongs to an earlier visit
  const settle = 1000; // ms to keep correcting while the layout fills in

  document.addEventListener('submit', () => {
    sessionStorage.setItem(key, JSON.stringify({ y: window.scrollY, at: Date.now() }));
  }, true);

  let mark = null;
  try {
    mark = JSON.parse(sessionStorage.getItem(key));
  } catch (e) {
    mark = null;
  }
  sessionStorage.removeItem(key);
  if (!mark || !mark.y || Date.now() - mark.at > fresh) return;

  let done = false;
  function stop() {
    done = true;
  }

  // Whatever the reader does themselves wins.
  for (const type of ['wheel', 'touchstart', 'keydown', 'mousedown']) {
    window.addEventListener(type, stop, { passive: true, once: true });
  }

  // The page is still growing while widgets, charts and fonts settle, and the
  // browser puts in its own restore once it has, so the position has to be
  // held for a moment rather than set once.
  const deadline = performance.now() + settle;
  function apply() {
    if (done) return;
    if (Math.abs(window.scrollY - mark.y) > 2) window.scrollTo(0, mark.y);
    if (performance.now() < deadline) requestAnimationFrame(apply);
  }
  apply();
})();
