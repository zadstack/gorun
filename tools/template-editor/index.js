import {basicSetup} from 'codemirror';
import {EditorView, keymap} from '@codemirror/view';
import {Prec} from '@codemirror/state';
import {completionStatus, currentCompletions, closeCompletion} from '@codemirror/autocomplete';
import {html} from '@codemirror/lang-html';
import {css} from '@codemirror/lang-css';

// Keep the native form as the source of truth for preview, CSRF and publication.
export function enhance(textarea) {
  const container = textarea.closest('[data-code-editor]');
  if (!container || container.dataset.enhanced) return;
  const exit = container.parentElement.querySelector('[data-code-exit]');
  const host = document.createElement('div');
  host.className = 'gp-code-host';
  container.append(host);
  let view;
  try {
    view = new EditorView({
      doc: textarea.value,
      parent: host,
      extensions: [
        // An unseen completion (a result with no visible options) would swallow
        // Escape; dismiss it and let the remaining Escape handlers run as usual.
        Prec.highest(keymap.of([{key:'Escape', run:view => {
          if (completionStatus(view.state) && !currentCompletions(view.state).length) closeCompletion(view);
          return false;
        }}])),
        basicSetup,
        textarea.dataset.language === 'css' ? css() : html(),
        EditorView.contentAttributes.of({'aria-label': textarea.dataset.label || 'Template source', 'aria-describedby': 'code-editor-help', spellcheck: 'false'}),
        EditorView.theme({
          '&': {height:'480px', background:'var(--panel, #fff)', color:'var(--ink, #18231e)'},
          '.cm-scroller': {overflow:'auto', fontFamily:'ui-monospace, monospace', fontSize:'13px', lineHeight:'1.6'},
          '.cm-content': {direction:'ltr', textAlign:'left'},
          '.cm-gutters': {background:'var(--panel-2, #f5f7f5)', color:'var(--muted, #627069)', borderColor:'var(--line, #ddd)'},
          '&.cm-focused': {outline:'2px solid var(--green, #16815a)', outlineOffset:'-2px'}
        }),
        keymap.of([{key:'Escape', run:() => {exit?.focus(); return true;}}]),
        EditorView.updateListener.of(update => {
          if (!update.docChanged) return;
          textarea.value = update.state.doc.toString();
          textarea.dispatchEvent(new Event('input', {bubbles:true}));
        })
      ]
    });
    textarea.hidden = true;
    container.querySelector('[data-code-gutter]')?.setAttribute('hidden', '');
    container.dataset.enhanced = 'true';
    // Programmatic form reset must reset the visible editor too.
    textarea.form?.addEventListener('reset', () => setTimeout(() => view.dispatch({changes:{from:0,to:view.state.doc.length,insert:textarea.value}}), 0));
    textarea.addEventListener('change', () => {
      if (textarea.value !== view.state.doc.toString()) view.dispatch({changes:{from:0,to:view.state.doc.length,insert:textarea.value}});
    });
  } catch (error) {
    view?.destroy(); host.remove(); textarea.hidden = false;
    throw error;
  }
}
