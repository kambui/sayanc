(function () {
  'use strict';
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const mouse = matchMedia('(hover: hover) and (pointer: fine)');
  document.querySelectorAll('.portfolio-link-card').forEach(card => {
    const overlay = card.querySelector('.portfolio-overlay');
    const typed = card.querySelector('.portfolio-typed');
    const text = card.querySelector('.portfolio-description').textContent;
    let hovered = false, focused = false, panel = 0, count = 0, frame = 0, last = 0;
    function draw() {
      overlay.style.transform = `translateX(${(panel - 1) * 100}%)`;
      typed.textContent = text.slice(0, Math.floor(count));
    }
    function tick(now) {
      const dt = Math.min(now - last, 50); last = now;
      const active = hovered || focused;
      if (reduced.matches) { panel = active ? 1 : 0; count = active ? text.length : 0; }
      else if (active) {
        if (panel < 1) panel = Math.min(1, panel + dt / 450);
        else count = Math.min(text.length, count + dt / 18);
      } else {
        if (count > 0) count = Math.max(0, count - dt / 9);
        else panel = Math.max(0, panel - dt / 450);
      }
      draw();
      if (active ? panel < 1 || count < text.length : panel > 0 || count > 0) frame = requestAnimationFrame(tick);
      else frame = 0;
    }
    function start() { if (!frame) { last = performance.now(); frame = requestAnimationFrame(tick); } }
    card.addEventListener('pointerenter', event => { if (mouse.matches && event.pointerType !== 'touch') { hovered = true; start(); } });
    card.addEventListener('pointerleave', () => { hovered = false; start(); });
    card.addEventListener('focus', () => { focused = card.matches(':focus-visible'); start(); });
    card.addEventListener('blur', () => { focused = false; start(); });
    reduced.addEventListener('change', start);
  });
}());
