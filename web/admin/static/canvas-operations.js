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
        body.append(chips(card));

        if (provider && !entry?.configured) body.append(el('small', entry?.reason || t('unconfigured')));

        const button = btn(t('review_run'), () => action(() => preview(card.id)), 'canvas-operation-button'); button.disabled = !entry || (provider && !entry.configured); body.append(button);
      }
      if (card.kind === 'schedule') {
        const state = document.schedules?.[card.id];
        body.append(el('p', t(card.interval)), el('p', state ? t(state.status) : t('schedule_inactive')));
        const next = state?.next_run_at && nextRun(state.next_run_at);
        if (next) body.append(next);
        if (state?.reason) body.append(el('p', state.reason));
        body.append(btn(t(state?.enabled ? 'disable_schedule' : 'enable_schedule'), () => toggleSchedule(card), 'canvas-operation-button'));
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
          media.addEventListener('error', () => { body.dataset.result = 'missing'; media.replaceWith(missing()); });
          body.dataset.result = 'ready'; body.append(media,el('small',t('private_media')));
        } else { body.dataset.result = 'missing'; body.append(missing()); }
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
    const lang = () => root.ownerDocument.documentElement.lang || undefined;
    const missing = () => { const box = el('p', undefined, 'canvas-result-missing'); box.append(el('span', t('media_error'))); return box; };
    // "Next 14:20 · in 42 minutes": the clock time first, the full date on hover, as the schedules panel shows it.
    function nextRun(iso) {
      const now = Date.now(), label = window.CanvasState?.nextRunLabel(iso, now, lang()); if (!label) return null;
      const time = el('time', label.kind === 'due' ? t('schedules_due') : `${t('schedules_next')} ${window.CanvasState.atLabel(iso, now, lang())} · ${label.text}`, 'canvas-schedule-next');
      time.dateTime = iso; time.title = new Date(iso).toLocaleString(lang()); return time;
    }
    // What feeds an agent or provider node, and the schedule that triggers it, read from the saved graph.
    function chips(card) {
      const document = api.document(), list = el('ul', undefined, 'canvas-node-chips'), number = new Intl.NumberFormat(lang());
      const inbound = (document?.edges || []).filter(edge => edge.to === card.id);
      list.dataset.chipsFor = card.id;
      const inputs = inbound.filter(edge => document.cards.find(c => c.id === edge.from)?.kind !== 'schedule').length;
      list.append(el('li', `${t('chip_inputs')}: ${number.format(inputs)}`));
      for (const edge of inbound) {
        const schedule = document.cards.find(c => c.id === edge.from && c.kind === 'schedule');
        if (schedule && document.schedules?.[schedule.id]?.enabled) { const chip = el('li', `${t('chip_scheduled')} · ${t(schedule.interval)}`); chip.dataset.chip = 'schedule'; list.append(chip); }
      }
      return list;
    }
    function updateChips() {
      for (const list of cardsEl.querySelectorAll('.canvas-node-chips')) {
        const card = api.document()?.cards.find(c => c.id === list.dataset.chipsFor);
        if (card) list.replaceWith(chips(card));
      }
    }
    // The one enable/disable path for a schedule node, shared by its card button and the schedules panel.
    function toggleSchedule(card) {
      return action(async () => {
        const state = api.document().schedules?.[card.id];
        if (!(await api.save())) throw new Error(t('save_first'));
        if (state?.enabled) { await json(`/schedules/${encodeURIComponent(card.id)}`, {enabled:false}); await api.reload(); }
        else { const edge = api.document().edges.find(edge => edge.from === card.id); if (!edge) throw new Error(t('connection_error')); await preview(edge.to, card.id); }
      });
    }
    let edgeSignature = null;
    function drawEdges() {
      const state = api.document(); if (!state) return;
      const warning=root.querySelector('[data-route-warning]');
      if(!['flow','lanes'].includes(root.dataset.view)||matchMedia('(max-width:767px)').matches){svg.replaceChildren();edgeSignature=null;if(warning)warning.hidden=true;return;}
      const width=surface.scrollWidth,height=surface.scrollHeight;
      const boxes=[...cardsEl.children].map(node=>({id:node.dataset.cardId,x:node.offsetLeft,y:node.offsetTop,width:node.offsetWidth,height:node.offsetHeight}));
      const edges=state.edges||[],kinds=window.CanvasState?window.CanvasState.edgeStates(state.cards||[],edges,states,state.schedules||{}):[];
      let blocked=false;const items=[];
      for(const pair of CanvasLayout.ports(boxes,edges)) {
        const points=CanvasLayout.route(pair,boxes);
        if(!points){blocked=true;continue;}
        const d=points.map((point,index)=>`${index?'L':'M'} ${point.x} ${point.y}`).join(' ');
        const edgeState=kinds[edges.findIndex(e=>e.from===pair.edge.from&&e.to===pair.edge.to)]||'idle';
        items.push({pair,points,d,state:edgeState});
      }
      // Rebuilding restarts the CSS dash/packet animations, so skip it when nothing visible changed.
      const signature=JSON.stringify([width,height,items.map(i=>[i.d,i.state,i.pair.edge.from,i.pair.edge.to]),blocked]);
      if(warning)warning.hidden=!blocked;
      if(signature===edgeSignature)return;
      edgeSignature=signature;
      svg.replaceChildren();
      svg.setAttribute('width',width);svg.setAttribute('height',height);
      const defs=documentNode('defs'),marker=documentNode('marker'),tip=documentNode('polygon');
      marker.id='canvas-edge-arrow';marker.setAttribute('viewBox','0 0 10 10');marker.setAttribute('refX','17');marker.setAttribute('refY','5');marker.setAttribute('markerWidth','9');marker.setAttribute('markerHeight','9');marker.setAttribute('markerUnits','userSpaceOnUse');marker.setAttribute('orient','auto-start-reverse');
      tip.setAttribute('points','0,0 10,5 0,10');tip.setAttribute('class','canvas-edge-tip');marker.append(tip);defs.append(marker);svg.append(defs);
      for(const {pair,points,d,state:edgeState} of items) {
        const path=documentNode('path');
        path.setAttribute('d',d);path.setAttribute('marker-end','url(#canvas-edge-arrow)');path.dataset.state=edgeState;
        path.dataset.from=pair.edge.from;path.dataset.to=pair.edge.to;svg.append(path);
        if(edgeState==='flowing'){const packet=documentNode('circle');packet.setAttribute('r','4');packet.setAttribute('class','canvas-edge-packet');packet.style.offsetPath=`path('${d}')`;svg.append(packet);}
        const mid=points[Math.floor((points.length-1)/2)],next=points[Math.floor((points.length-1)/2)+1]||mid,mx=(mid.x+next.x)/2,my=(mid.y+next.y)/2;
        if(edgeState==='failed'){const mark=documentNode('text');mark.setAttribute('x',mx);mark.setAttribute('y',my);mark.setAttribute('class','canvas-edge-mark');mark.setAttribute('text-anchor','middle');mark.setAttribute('dominant-baseline','central');mark.textContent='\u00d7';svg.append(mark);}
        if(edgeState==='scheduled'){const clock=documentNode('g'),face=documentNode('circle'),hands=documentNode('path');clock.setAttribute('class','canvas-edge-clock');clock.setAttribute('transform',`translate(${mx} ${my})`);face.setAttribute('r','9');hands.setAttribute('d','M0 -4.5 V0 L3 2');clock.append(face,hands);svg.append(clock);}
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
        try { document.edges = CanvasGraph.add(document.cards, document.edges, {from:from.value,to:to.value}); api.changed(); renderConnections(); updateChips(); drawEdges(); announce(''); }
        catch (_) { announce(t('connection_error')); }
      }); add.disabled = !sources.length || !targets.length;
      row.append(label(t('from'), from), label(t('to'), to), add); connections.append(row);
      const list = el('ul', undefined, 'canvas-connection-list');
      for (const edge of document.edges) {
        const title = id => document.cards.find(c => c.id === id)?.title || t('unavailable');
        const item = el('li'); item.append(el('span', `${title(edge.from)} → ${title(edge.to)}`));
        item.append(btn(t('disconnect'), () => { document.edges = document.edges.filter(e => e.from !== edge.from || e.to !== edge.to); api.changed(); renderConnections(); updateChips(); drawEdges(); })); list.append(item);
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
      const data = await json('/runs'); runs = data.runs || []; renderHistory(); const first = !primed; primed = true; applyRunStates(first); schedulePoll();
    }
    // Poll only while a run is active and the tab is visible; errors are swallowed and retried on the next tick.
    let pollTimer = 0;
    function schedulePoll() {
      clearTimeout(pollTimer); pollTimer = 0;
      if (window.CanvasState && window.CanvasState.isActive(runs) && !window.document.hidden) pollTimer = setTimeout(() => { refreshHistory().catch(schedulePoll); }, 4000);
    }
    window.document.addEventListener('visibilitychange', () => {
      if (!window.document.hidden && window.CanvasState && window.CanvasState.isActive(runs)) refreshHistory().catch(schedulePoll);
    });
    function runAction(run) {
      if (run.status === 'waiting') return btn(t('check_result'), () => action(async () => { await json(`/runs/${encodeURIComponent(run.id)}/check`, {}); await refreshHistory(); }));
      if (run.status === 'waiting_tool') return btn(t('tool_review'), () => action(() => reviewTool(run)));
      if (run.status === 'requested') return btn(t('review_run'), () => action(() => preview(run.node_id)));
      return null;
    }
    const live = el('div', undefined, 'canvas-state-live canvas-sr-only'); live.setAttribute('aria-live', 'polite'); root.append(live);
    const lastStates = new Map(); let doneTimer = 0, primed = false, states = {};
    const stateWord = state => t(state === 'done' ? 'state_done' : state === 'waiting' ? 'state_waiting' : state);
    function applyRunStates(silent = false) {
      const now = Date.now(); states = window.CanvasState ? window.CanvasState.nodeStates(runs, now) : {};
      clearTimeout(doneTimer);
      const expiry = window.CanvasState ? window.CanvasState.doneExpiry(runs, now) : null;
      if (expiry !== null) doneTimer = setTimeout(applyRunStates, Math.max(expiry - now, 0) + 50);
      const newest = new Map(); for (const run of runs) if (run.node_id && !newest.has(run.node_id)) newest.set(run.node_id, run);
      const messages = [];
      for (const card of cardsEl.querySelectorAll('[data-card-id]')) {
        const id = card.dataset.cardId, state = states[id] || '', heading = card.querySelector('.canvas-card-heading');
        let span = heading && heading.querySelector(':scope > .canvas-node-state');
        extra(card, heading, state, newest.get(id));
        if (!state) { delete card.dataset.state; span?.remove(); } else if (heading) {
          card.dataset.state = state;
          if (!span) { span = el('span', undefined, 'canvas-node-state'); heading.append(span); }
          const run = newest.get(id); let text = stateWord(state);
          if (state === 'running') { const s = Math.max(0, Math.floor((now - Date.parse(run.created_at)) / 1000)); text += ` · ${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`; }
          const key = `${text}|${state}|${run.id}|${run.status}`;
          if (span.dataset.key !== key) {
            span.dataset.key = key; span.replaceChildren(text);
            if (state === 'waiting') {
              const act = runAction(run);
              if (act) { const title = card.querySelector('h2')?.textContent || ''; if (title) act.setAttribute('aria-label', `${act.textContent}: ${title}`); span.append(' ', act); }
            }
          }
        }
        if ((lastStates.get(id) || '') !== state) {
          if (state || lastStates.has(id)) messages.push(`${card.querySelector('h2')?.textContent || ''}: ${state ? stateWord(state) : ''}`);
          if (state) lastStates.set(id, state); else lastStates.delete(id);
        }
      }
      if (messages.length && !silent) live.textContent = messages.filter(m => !m.endsWith(': ')).join('. ');
      const counter = root.querySelector('[data-run-count]'), active = Object.values(states).filter(s => s === 'running' || s === 'queued').length;
      if (counter) { counter.hidden = !active; counter.textContent = active ? `${t('runs_active')}: ${new Intl.NumberFormat(lang()).format(active)}` : ''; }
      drawEdges();
    }
    // One line under a node's heading: why its latest run failed, or a short skeleton while an agent or provider runs.
    function extra(card, heading, state, run) {
      let line = card.querySelector(':scope > .canvas-node-extra');
      const writing = state === 'running' && ['agent','provider'].includes(card.dataset.kind);
      const reason = state === 'failed' && run?.error ? (/^run_[a-z_]+$/.test(run.error) ? t(run.error) : run.error) : '';
      const key = writing ? 'writing' : reason ? `failed|${reason}` : '';
      if (!key || !heading) { line?.remove(); return; }
      if (line?.dataset.key === key) return;
      if (!line) { line = el(writing ? 'div' : 'p', undefined, 'canvas-node-extra'); heading.after(line); }
      else if (line.tagName !== (writing ? 'DIV' : 'P')) { const next = el(writing ? 'div' : 'p', undefined, 'canvas-node-extra'); line.replaceWith(next); line = next; }
      line.dataset.key = key; line.dataset.kind = writing ? 'writing' : 'failed';
      if (writing) { const label = el('small', t('state_writing')); line.replaceChildren(label, el('span'), el('span'), el('span')); for (const bar of line.querySelectorAll('span')) bar.setAttribute('aria-hidden', 'true'); }
      else line.replaceChildren(reason);
    }
    // Polling calls this every few seconds: skip the rebuild when nothing shown has changed, and keep focus on
    // the same run's control (data-run-id + data-action) when it must rebuild, as canvas-schedules.js does.
    let historySignature = null;
    const tag = (control, runID, name) => { if (control) { control.dataset.runId = runID; control.dataset.action = name; } return control; };
    function renderHistory() {
      const signature = JSON.stringify(runs.map(run => [run.id, run.status, run.error || '', run.result_card_id || '', run.tool_calls_attempted || 0]));
      if (signature === historySignature) return;
      historySignature = signature;
      const active = history.contains(window.document.activeElement) ? window.document.activeElement : null;
      const focusKey = active?.dataset.action ? [active.dataset.action, active.dataset.runId || ''] : null;
      history.replaceChildren(tag(btn(t('refresh_runs'), () => action(refreshHistory)), '', 'refresh'));
      if (!runs.length) history.append(el('p', t('no_runs')));
      const list = el('ol', undefined, 'canvas-run-list');
      for (const run of runs) {
        const item = el('li'); item.append(el('strong', t(run.status)), el('span', [run.provider_title,run.actor_name].filter(Boolean).join(' · ')), el('small', new Date(run.created_at).toLocaleString()));
        if (run.tool_calls_attempted) item.append(el('p', `${t('tool_attempts')}: ${run.tool_calls_attempted}. ${t('tool_effect_warning')}`));
        if (run.error) item.append(el('p', /^run_[a-z_]+$/.test(run.error) ? t(run.error) : run.error));
        const act = tag(runAction(run), run.id, 'run'); if (act) item.append(act);
        if (['requested','queued','waiting_agent','waiting','waiting_tool'].includes(run.status)) item.append(tag(btn(t('cancel_run'), () => action(async () => {
          if (!window.confirm(t('cancel_run_help'))) return;
          await json(`/runs/${encodeURIComponent(run.id)}/cancel`, {}); await refreshHistory(); announce(t('cancelled'));
        })), run.id, 'cancel'));
        if (run.result_card_id) item.append(tag(btn(t('show_result'), () => api.reload()), run.id, 'result')); list.append(item);
      }
      history.append(list);
      if (focusKey) [...history.querySelectorAll('[data-action]')].find(c => c.dataset.action === focusKey[0] && c.dataset.runId === focusKey[1])?.focus({preventScroll: true});
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
    function render() { renderPreset(); renderConnections(); renderSharing(); applyRunStates(); }
    new ResizeObserver(drawEdges).observe(cardsEl);
    loadOptions();
    function saved() {
      for (const card of api.document()?.cards || []) if (card.kind === 'schedule') {
        const element = [...cardsEl.children].find(el => el.dataset.cardId === card.id)?.querySelector('.canvas-card-body');
        if (element) { element.replaceChildren(); cardBody(element, card); }
      }
      updateChips(); drawEdges();
    }
    return {editCard,readCard,cardBody,drawEdges,render,saved,applyRunStates,toggleSchedule};
  }
})();
