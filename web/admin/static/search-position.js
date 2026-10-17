// Keep the search enhancement lazy; the native form and CSS dropdown remain usable.
const form = document.querySelector('.admin-search-form');
const input = form?.querySelector('input[type="search"]');
const panel = form?.querySelector('#admin-search-results');
if (input && panel) {
  let ready = false, stop;
  input.addEventListener('focus', async () => {
    if (ready) return;
    ready = true;
    try {
      const {computePosition, autoUpdate, offset, flip, shift, size} = await import('./vendor/floating-ui-dom-1.7.4.js');
      const update = () => {
        if (!panel.getClientRects().length) return;
        computePosition(input, panel, {
          placement: 'bottom-start', strategy: 'fixed',
          middleware: [offset(4), flip({padding:8}), shift({padding:8}), size({padding:8, apply({availableHeight, availableWidth, rects, elements}) {
            Object.assign(elements.floating.style, {width: `${Math.min(rects.reference.width, availableWidth)}px`, maxHeight: `${Math.max(0,Math.min(384, availableHeight))}px`});
          }})]
        }).then(({x,y}) => Object.assign(panel.style,{position:'fixed',inset:'auto',margin:'0',left:`${x}px`,top:`${y}px`}));
      };
      const sync = () => {
        const visible = panel.getClientRects().length > 0;
        input.setAttribute('aria-expanded', String(visible));
        if (visible && !stop) stop = autoUpdate(input,panel,update);
        if (!visible && stop) {stop();stop=null;}
        if (visible) update();
      };
      const observer = new MutationObserver(sync);
      observer.observe(panel,{childList:true,subtree:true});
      input.addEventListener('focus',()=>requestAnimationFrame(sync));
      form.addEventListener('htmx:afterSwap',()=>requestAnimationFrame(sync));
      form.addEventListener('keydown',()=>requestAnimationFrame(sync));
      document.addEventListener('click',()=>requestAnimationFrame(sync));
      sync();
      addEventListener('pagehide',()=>{stop?.();observer.disconnect();},{once:true});
    } catch { ready = false; }
  });
}
