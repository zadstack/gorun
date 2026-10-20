(() => {
  'use strict';
  window.CanvasOperations = {create};
  function create(api) {
    const {root, t, element: el, button: btn, request, csrf, form, surface, cardsEl} = api;
    const base = `/admin/canvases/${encodeURIComponent(root.dataset.id)}/operations`;
    const host = root.querySelector('[data-canvas-operations]');
    let options = {types: [], providers: [], agents: [], can_share: false}, optionsReady = false, grants = [], runs = [], review = null, scheduleID = '', busy = false;
    const message = el('p', t('operations_loading'), 'canvas-ops-message'); message.setAttribute('role', 'status');
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg'); svg.classList.add('canvas-edges'); svg.setAttribute('aria-hidden', 'true'); surface.prepend(svg);
    const section = (name, open = false) => { const d = el('details', undefined, 'canvas-ops-section'); d.open = open; d.append(el('summary', t(name))); const body = el('div', undefined, 'canvas-ops-section-body'); d.append(body); host.append(d); return body; };
    host.append(message);
    const connections = section('connections', true), history = section('history'), sharing = section('sharing');
    const reviewDialog = el('dialog', undefined, 'canvas-dialog canvas-review'); reviewDialog.setAttribute('aria-label', t('review')); root.append(reviewDialog);
    let toolEpoch = 0;
    reviewDialog.addEventListener('close', () => { toolEpoch++; });
    window.addEventListener('pagehide', () => { toolEpoch++; if (reviewDialog.open) reviewDialog.close(); });
    const textField = (title, value) => { const box = el('div', undefined, 'canvas-review-field'); box.append(el('h3', title), el('pre', value || '—')); return box; };
    const select = (name, items, value = '') => { const input = el('select'); input.name = name; for (const item of items) { const opt = el('option', item.title); opt.value = item.id; input.append(opt); } if (value) input.value = value; return input; };
    const label = (title, input) => { const box = el('label', title); box.append(input); return box; };
    const announce = text => { message.textContent = text; };
    async function json(path, body) {
      const response = await request(base + path, body === undefined ? {} : {method:'POST', headers:{'Content-Type':'application/json','X-CSRF-Token':csrf}, body:JSON.stringify(body)});
      let data; try { data = await response.json(); } catch (_) { throw new Error(t('operations_error')); }
      if (!response.ok) {
        const error = new Error(data.message || (response.status === 409 ? t('conflict') : t('operations_error')));
        if (data.error === 'reauth_required') { try { const url = new URL(data.reauth_url, location.origin); if (url.origin === location.origin && url.pathname === '/admin/reauth') error.reauthURL = url.href; } catch (_) {} }
        throw error;
      }
      return data;
    }
    async function action(fn) {
      if (busy) return;
      busy = true; host.setAttribute('aria-busy', 'true');
      try { await fn(); } catch (error) { announce(error.message || t('operations_error')); }
      finally { busy = false; host.removeAttribute('aria-busy'); }
    }
    function fill(input, items, value) {
      const entries = [...items];
      if (value && !entries.some(item => item.id === value)) entries.unshift({id:value,title:t('unconfigured')});
      const replacement = select(input.name, entries.length ? entries : [{id:'',title:t('none')}], value);
      input.replaceChildren(...replacement.childNodes); if (value) input.value = value;
    }
    async function loadOptions() {
      try {
        options = await json('/options'); optionsReady = true;
        options.types ||= []; options.providers ||= []; options.agents ||= []; options.presets ||= [];
        announce(''); if (api.document()) api.render(); else render(); await refreshHistory();
        if (options.can_share) { const data = await json('/grants'); grants = data.grants || []; renderSharing(); }
      } catch (_) { announce(t('operations_error')); }
    }
    function editCard(editor, card) {
      fill(editor.elements.source_type, options.types, card.source_type);
      editor.elements.source_limit.value = card.source_limit || 3;
      editor.elements.generation_kind.value = card.generation_kind || 'text';
      providerChoices(card.provider_id, card.preset_id);
      fill(editor.elements.agent_id, options.agents, card.agent_id);
      editor.elements.interval.value = card.interval || 'daily';
    }
    function providerChoices(providerID = '', presetID = '') {
      const kind = form.elements.generation_kind.value;
      fill(form.elements.provider_id, [{id:'',title:t('preset_incomplete')},...options.providers.filter(p => (p.kind || 'text') === kind).map(p => ({...p,title:p.title + (p.configured ? '' : ` · ${t('unconfigured')}`)}))], providerID);
      fill(form.elements.preset_id, [{id:'',title:t('no_skill')},...(options.presets || []).filter(p => p.kind === kind)], presetID);
    }
    form.elements.generation_kind.addEventListener('change', () => providerChoices());
    function readCard(editor, card) {
      if (card.kind === 'source') { card.source_type = editor.elements.source_type.value; card.source_limit = Number(editor.elements.source_limit.value); return optionsReady && Number.isInteger(card.source_limit) && card.source_limit >= 1 && card.source_limit <= 5; }
      if (card.kind === 'provider') { card.provider_id = editor.elements.provider_id.value; card.generation_kind = editor.elements.generation_kind.value; card.preset_id = editor.elements.preset_id.value; return optionsReady; }
      if (card.kind === 'agent') { card.agent_id = editor.elements.agent_id.value; return optionsReady; }
      if (card.kind === 'schedule') { card.interval = editor.elements.interval.value; return ['hourly','daily','weekly'].includes(card.interval); }
      if (card.kind === 'prompt') return !!card.body.trim();
      return true;
    }
    function cardBody(body, card) {
      const document = api.document();

      if (card.kind === 'source') body.append(el('p', `${options.types.find(p => p.id === card.source_type)?.title || card.source_type || t('preset_incomplete')} · ${card.source_limit}`), el('small', t('source_help')));
      if (card.kind === 'provider' || card.kind === 'agent') {
        const provider = card.kind === 'provider';
        const entry = (provider ? options.providers : options.agents).find(p => p.id === (provider ? card.provider_id : card.agent_id));
        body.append(el('p', entry?.title || t('preset_incomplete')));
        if (provider) body.append(el('small', t(card.generation_kind || 'text')));

        if (provider && !entry?.configured) body.append(el('small', entry?.reason || t('unconfigured')));

        const button = btn(t('review_run'), () => action(() => preview(card.id)), 'canvas-operation-button'); button.disabled = !entry || (provider && !entry.configured); body.append(button);
      }
      if (card.kind === 'schedule') {
        const state = document.schedules?.[card.id];
        body.append(el('p', t(card.interval)), el('p', state ? t(state.status) : t('schedule_inactive')));
        if (state?.next_run_at) body.append(el('small', new Date(state.next_run_at).toLocaleString()));
        if (state?.reason) body.append(el('p', state.reason));
        body.append(btn(t(state?.enabled ? 'disable_schedule' : 'enable_schedule'), () => action(async () => {
          if (!(await api.save())) throw new Error(t('save_first'));
          if (state?.enabled) { await json(`/schedules/${encodeURIComponent(card.id)}`, {enabled:false}); await api.reload(); }
          else { const edge = api.document().edges.find(edge => edge.from === card.id); if (!edge) throw new Error(t('connection_error')); await preview(edge.to, card.id); }
        }), 'canvas-operation-button'));
      }
      if (card.kind === 'result') {
        const result = document.results?.[card.run_id];
        const expected = `${base}/runs/${encodeURIComponent(card.run_id)}/media`;
        let url; try { url = new URL(result?.url || '', location.origin); } catch (_) {}
        if (result && url?.origin === location.origin && url.pathname === expected && ['image','video','render'].includes(result.kind)) {
          const media = el(result.kind === 'image' ? 'img' : 'video');
          media.src = url.href; media.className = 'canvas-result-media';
          if (result.kind === 'image') { media.alt = card.title; media.loading = 'lazy'; }
          else { media.controls = true; media.preload = 'metadata'; media.playsInline = true; }
          media.addEventListener('error', () => { media.replaceWith(el('p', t('media_error'))); });
          body.append(media,el('small',t('private_media')));
        } else body.append(el('p', t('media_error')));
      }
      const provenance = document.provenance?.[card.id];
      if (provenance) {
        const source = el('small', undefined, 'canvas-provenance');
        const actor = el('bdi', provenance.actor_name);
        const date = new Date(provenance.created_at);
        const time = el('time', Number.isFinite(date.getTime()) ? date.toLocaleString(root.ownerDocument.documentElement.lang || undefined) : String(provenance.created_at || ''));
        time.dateTime = provenance.created_at; time.dir = 'auto';
        source.append(root.ownerDocument.createTextNode(t('attributed') + ' '), actor, root.ownerDocument.createTextNode(' · '), time); body.append(source);
      }
    }
    function drawEdges() {
      const state = api.document(); if (!state) return;
      svg.replaceChildren();
      const warning=root.querySelector('[data-route-warning]'); if(warning)warning.hidden=true;
      if(root.dataset.view!=='flow'||matchMedia('(max-width:767px)').matches)return;
      svg.setAttribute('width',surface.scrollWidth);svg.setAttribute('height',surface.scrollHeight);
      const boxes=[...cardsEl.children].map(node=>({id:node.dataset.cardId,x:node.offsetLeft,y:node.offsetTop,width:node.offsetWidth,height:node.offsetHeight}));
      for(const pair of CanvasLayout.ports(boxes,state.edges||[])) {
        const points=CanvasLayout.route(pair,boxes);
        if(!points){if(warning)warning.hidden=false;continue;}
        const path=documentNode('path');
        path.setAttribute('d',points.map((point,index)=>`${index?'L':'M'} ${point.x} ${point.y}`).join(' '));
        path.dataset.from=pair.edge.from;path.dataset.to=pair.edge.to;svg.append(path);
        for(const [end,point] of [['from',pair.from],['to',pair.to]]) {
          const dot=documentNode('circle');dot.setAttribute('cx',point.x);dot.setAttribute('cy',point.y);dot.setAttribute('r','4.25');dot.dataset.port=end;dot.dataset.cardId=pair.edge[end];svg.append(dot);
        }
      }
    }
    const documentNode = tag => document.createElementNS('http://www.w3.org/2000/svg', tag);
    function renderConnections() {
      const document = api.document(); if (!document) return;
      connections.replaceChildren();
      const sources = document.cards.filter(c => ['source','content','prompt','note','schedule'].includes(c.kind));
      const targets = document.cards.filter(c => ['provider','agent'].includes(c.kind));
      const from = select('edge_from', sources), to = select('edge_to', targets);
      const row = el('div', undefined, 'canvas-connect-controls');
      const add = btn(t('connect'), () => {
        try { document.edges = CanvasGraph.add(document.cards, document.edges, {from:from.value,to:to.value}); api.changed(); renderConnections(); drawEdges(); announce(''); }
        catch (_) { announce(t('connection_error')); }
      }); add.disabled = !sources.length || !targets.length;
      row.append(label(t('from'), from), label(t('to'), to), add); connections.append(row);
      const list = el('ul', undefined, 'canvas-connection-list');
      for (const edge of document.edges) {
        const title = id => document.cards.find(c => c.id === id)?.title || t('unavailable');
        const item = el('li'); item.append(el('span', `${title(edge.from)} → ${title(edge.to)}`));
        item.append(btn(t('disconnect'), () => { document.edges = document.edges.filter(e => e.from !== edge.from || e.to !== edge.to); api.changed(); renderConnections(); drawEdges(); })); list.append(item);
      }
      connections.append(list); if (!document.edges.length) connections.append(el('p', t('no_connections')));
    }
    async function preview(nodeID, schedule = '') {
      if (!(await api.save())) throw new Error(t('save_first'));
      review = await json('/preview', {version:api.document().version,node_id:nodeID}); scheduleID = schedule;
      const heading = el('header'); heading.append(el('h2', t('review')), btn(t('cancel'), () => reviewDialog.close()));
      const content = el('div', undefined, 'canvas-review-content');
      content.append(heading, textField(t('destination'), [review.provider?.title,review.provider?.model,review.provider?.endpoint].filter(Boolean).join('\n')), textField(t('prompt'), review.prompt), textField(t('context'), review.context));
      if (review.managed_tools?.length) content.append(textField(t('tools_allowlist'), review.managed_tools.join('\n')), el('p', t('tools_manual_warning')));
      if (schedule) { const node = api.document().cards.find(c => c.id === schedule); content.append(el('p', `${t('interval')}: ${t(node?.interval || '')}`)); }
      const consent = el('input'); consent.type = 'checkbox'; const consentLabel = label(t(schedule ? 'recurring_consent' : 'consent'), consent); consentLabel.className = 'canvas-review-consent';
      const error = el('p'); error.setAttribute('role', 'alert');
      const requestID = crypto.randomUUID(); let attempted = false;
      const runButton = btn(t(schedule ? 'enable_schedule' : 'run'), async () => {
        if (attempted) return; attempted = true; runButton.disabled = true;
        try {
          if (!consent.checked || !(await api.save())) throw new Error(t('save_first'));
          if (scheduleID) await json(`/schedules/${encodeURIComponent(scheduleID)}`, {enabled:true,request_id:requestID,review_token:review.review_token});
          else await json('/runs', {request_id:requestID,review_token:review.review_token});
          reviewDialog.close(); announce(t('run_confirmed')); await refreshHistory(); if (scheduleID) await api.reload();
        } catch (err) { error.textContent = err.message || t('operations_error'); }
      }, 'canvas-primary');
      runButton.disabled = true; consent.addEventListener('change', () => { runButton.disabled = attempted || !consent.checked; });
      content.append(consentLabel,error,runButton); reviewDialog.replaceChildren(content); reviewDialog.showModal();
    }
    async function reviewTool(run) {
      const epoch = ++toolEpoch;
      const loading = el('div', undefined, 'canvas-review-content'); loading.append(el('p', t('operations_loading')), btn(t('cancel'), () => reviewDialog.close()));
      reviewDialog.replaceChildren(loading); reviewDialog.showModal();
      let data;
      try { data = await json(`/runs/${encodeURIComponent(run.id)}/tool`); } catch (err) { if (epoch === toolEpoch && reviewDialog.open) { loading.firstChild.textContent = err.message || t('operations_error'); } return; }
      if (epoch !== toolEpoch || !reviewDialog.open) return;
      const content = el('div', undefined, 'canvas-review-content');
      const heading = el('header'), title = el('h2', t('tool_review')); title.tabIndex = -1; heading.append(title, btn(t('cancel'), () => reviewDialog.close()));
      content.append(heading, textField(t('destination'), `${data.destination} · ${data.host}`), textField(t('tool_name'), data.tool), textField(t('tool_arguments'), JSON.stringify(data.arguments, null, 2)), el('p', t('tool_effect_warning')), el('small', `${t('tool_expires')}: ${new Date(data.expires_at).toLocaleString()}`));
      const consent = el('input'); consent.type = 'checkbox';
      const error = el('p'); error.setAttribute('role', 'alert');
      let attempted = false;
      async function decide(approve) {
        if (attempted || epoch !== toolEpoch || !reviewDialog.open || (approve && !consent.checked)) return;
        attempted = true; approveButton.disabled = rejectButton.disabled = true; consent.disabled = true;
        try {
          await json(`/runs/${encodeURIComponent(run.id)}/tool`, {approval_id:data.approval_id, revision:data.revision, approve});
          if (epoch !== toolEpoch) return;
          reviewDialog.close(); announce(t(approve ? 'tool_approved' : 'cancelled')); await refreshHistory();
        } catch (err) {
          if (epoch !== toolEpoch || !reviewDialog.open) return;
          error.textContent = err.message || t('operations_error');
          if (err.reauthURL) { const link = el('a', t('tool_reauthenticate')); link.href = err.reauthURL; error.append(' ', link); }
        }
      }
      const approveButton = btn(t('tool_approve'), () => decide(true), 'canvas-primary');
      const rejectButton = btn(t('tool_reject'), () => decide(false));
      approveButton.disabled = true; consent.addEventListener('change', () => { approveButton.disabled = attempted || !consent.checked; });
      content.append(label(t('tool_exact_consent'), consent), error, approveButton, rejectButton);
      reviewDialog.replaceChildren(content); title.focus();
    }
    async function refreshHistory() {
      const data = await json('/runs'); runs = data.runs || []; renderHistory();
    }
    function renderHistory() {
      history.replaceChildren(btn(t('refresh_runs'), () => action(refreshHistory)));
      if (!runs.length) history.append(el('p', t('no_runs')));
      const list = el('ol', undefined, 'canvas-run-list');
      for (const run of runs) {
        const item = el('li'); item.append(el('strong', t(run.status)), el('span', [run.provider_title,run.actor_name].filter(Boolean).join(' · ')), el('small', new Date(run.created_at).toLocaleString()));
        if (run.tool_calls_attempted) item.append(el('p', `${t('tool_attempts')}: ${run.tool_calls_attempted}. ${t('tool_effect_warning')}`));
        if (run.error) item.append(el('p', run.error === 'run_stopped' ? t('run_stopped') : run.error));
        if (run.status === 'waiting') item.append(btn(t('check_result'), () => action(async () => { await json(`/runs/${encodeURIComponent(run.id)}/check`, {}); await refreshHistory(); })));
        if (run.status === 'waiting_tool') item.append(btn(t('tool_review'), () => action(() => reviewTool(run))));
        if (run.status === 'requested') item.append(btn(t('review_run'), () => action(() => preview(run.node_id))));
        if (['requested','queued','waiting_agent','waiting','waiting_tool'].includes(run.status)) item.append(btn(t('cancel_run'), () => action(async () => {
          if (!window.confirm(t('cancel_run_help'))) return;
          await json(`/runs/${encodeURIComponent(run.id)}/cancel`, {}); await refreshHistory(); announce(t('cancelled'));
        })));
        if (run.result_card_id) item.append(btn(t('show_result'), () => api.reload())); list.append(item);
      }
      history.append(list);
    }
    function renderSharing() {
      sharing.replaceChildren(); sharing.parentElement.hidden = !options.can_share;
      if (!options.can_share) return;
      sharing.append(el('p', t('sharing_help')));
      if (!options.agents.length) { sharing.append(el('p', t('none'))); return; }
      const agent = select('grant_agent', options.agents), fields = el('div', undefined, 'canvas-grant-fields');
      sharing.append(label(t('agent'), agent), fields);
      function selectAgent() {
        fields.replaceChildren(); const grant = grants.find(g => g.agent_id === agent.value) || {};
        const toggles = {};
        for (const [key, text] of [['read','read_grant'],['append','append_grant'],['run','run_grant']]) { const input = el('input'); input.type = 'checkbox'; input.checked = !!grant[key]; toggles[key] = input; const row = label(t(text), input); row.className = 'canvas-grant-check'; fields.append(row); }
        const nodes = el('fieldset'); nodes.append(el('legend', t('shared_nodes')));
        for (const card of api.document()?.cards || []) if (['note','content','source','prompt'].includes(card.kind)) { const input = el('input'); input.type = 'checkbox'; input.value = card.id; input.checked = (grant.node_ids || []).includes(card.id); const row = label(card.title, input); row.className = 'canvas-grant-check'; nodes.append(row); }
        fields.append(nodes);
        async function persist(revoke) {
          if (!(await api.save())) throw new Error(t('save_first'));
          await json('/grants', {agent_id:agent.value,read:!revoke && toggles.read.checked,append:!revoke && toggles.append.checked,run:!revoke && toggles.run.checked,node_ids:revoke ? [] : [...nodes.querySelectorAll('input:checked')].map(input => input.value)});
          const data = await json('/grants'); grants = data.grants || []; announce(t('grant_saved')); selectAgent();
        }
        const actions = el('div', undefined, 'canvas-grant-actions'); actions.append(btn(t('save_grant'), () => action(() => persist(false))),btn(t('revoke_grant'), () => action(() => persist(true)))); fields.append(actions);
      }
      agent.addEventListener('change', selectAgent); selectAgent();
    }
    let presetRendered = false;
    function renderPreset() {
      if (presetRendered || !api.document()) return;
      presetRendered = true;
      const id = new URL(location.href).searchParams.get('preset'), document = api.document();
      if (!window.CanvasPresets?.ids.includes(id) || document.version !== 1 || document.cards.length || document.edges.length) return;
      const box = root.querySelector('[data-canvas-preset]'); box.className = 'canvas-preset-setup';
      box.append(el('h2', t(id)), el('p', t('preset_help')));
      const target = select('preset_target', [{id:'provider',title:t('provider')},{id:'agent',title:t('agent')}]);
      const apply = btn(t('apply_preset'), () => action(async () => {
        const data = CanvasPresets.build(id, {locale:root.closest('[lang]')?.lang || documentNodeLanguage(), targetKind:target.value});
        if (!api.applyPreset(data)) throw new Error(t('conflict'));
        box.hidden = true;
        if (!(await api.save())) throw new Error(t('save_first'));
        const url = new URL(location.href); url.searchParams.delete('preset'); window.history.replaceState(null, '', url); announce(t('preset_ready'));
      }), 'canvas-primary');
      box.append(label(t('preset_target'), target),apply);
    }
    const documentNodeLanguage = () => window.document.documentElement.lang;
    function render() { renderPreset(); renderConnections(); renderSharing(); drawEdges(); }
    new ResizeObserver(drawEdges).observe(cardsEl);
    loadOptions();
    function saved() {
      for (const card of api.document()?.cards || []) if (card.kind === 'schedule') {
        const element = [...cardsEl.children].find(el => el.dataset.cardId === card.id)?.querySelector('.canvas-card-body');
        if (element) { element.replaceChildren(); cardBody(element, card); }
      }
      drawEdges();
    }
    return {editCard,readCard,cardBody,drawEdges,render,saved};
  }
})();
