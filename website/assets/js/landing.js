/* Landing page behaviour: the intercept hold tray (a toy of the real Intercept flow),
   the real recording's controls and chapters, and the workflow stage tabs.
   Nothing here touches the network. Every piece enhances markup that already
   works without JavaScript, and every motion is skipped under reduced motion. */
(() => {
  'use strict';

  const doc = document;
  const root = doc.documentElement;
  const motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
  const darkQuery = window.matchMedia('(prefers-color-scheme: dark)');
  const reducedMotion = () => motionQuery.matches;

  function currentTheme() {
    const theme = root.dataset.theme;
    if (theme === 'dark' || theme === 'light') return theme;
    return darkQuery.matches ? 'dark' : 'light';
  }

  // Calls fn(theme) when the effective theme changes: the themechange event from
  // site.js, a data-theme change made any other way, or the system preference.
  function watchTheme(fn) {
    let last = currentTheme();
    const check = () => {
      const next = currentTheme();
      if (next === last) return;
      last = next;
      fn(next);
    };
    doc.addEventListener('themechange', check);
    new MutationObserver(check).observe(root, { attributes: true, attributeFilter: ['data-theme'] });
    darkQuery.addEventListener('change', check);
  }

  // Restarts a one-shot CSS animation keyed on a class.
  function replay(el, className) {
    el.classList.remove(className);
    void el.offsetWidth;
    el.classList.add(className);
  }

  /* ------------------------------------------------------------------------
     The hold tray. Phases:
       idle    intercept off; a short burst of shop traffic passes Kirby
       armed   intercept on; the next request is on its way to Kirby
       held    Kirby holds GET /api/v1/orders/1042; the id is editable
       busy    a forwarded request and its response are on the wire
       result  own order, another customer's order (a finding), 404, or dropped
     The switch only decides what happens to the next request, as in the real
     Intercept: a request already held stays held until Forward or Drop.
     ------------------------------------------------------------------------ */
  function initTray(tray) {
    if (!tray) return;
    const pick = (selector) => tray.querySelector(selector);
    const ui = {
      switch: pick('[data-tray-switch]'),
      switchState: pick('[data-tray-switch-state]'),
      wire: pick('[data-tray-wire]'),
      track: pick('[data-tray-track]'),
      kirbyBox: pick('[data-tray-kirbybox]'),
      kirby: pick('[data-tray-kirby]'),
      suck: pick('[data-tray-suck]'),
      msg: pick('[data-tray-msg]'),
      request: pick('[data-tray-request]'),
      id: pick('[data-tray-id]'),
      code: pick('[data-tray-code]'),
      wait: pick('[data-tray-wait]'),
      res: pick('[data-tray-res]'),
      status: pick('[data-tray-status]'),
      live: pick('[data-tray-live]'),
      finding: pick('[data-tray-finding]'),
      forward: pick('[data-tray-action="forward"]'),
      drop: pick('[data-tray-action="drop"]'),
      reset: pick('[data-tray-action="reset"]'),
    };
    if (Object.values(ui).some((el) => !el)) return;

    const OWN_ORDER = 1042;
    const OWN_CUSTOMER = 'cust_7731';
    const OTHER_CUSTOMER = 'cust_8812'; // order 1043's owner in the real recording
    const SPEED = 200; // px per second for passing traffic
    const LEG = 440; // ms for a forwarded request, and again for its response
    const GAP = 18; // px between requests on the wire
    const AMBIENT = ['GET /', 'GET /api/v1/products', 'POST /api/v1/cart'];
    const TARGET = `GET /api/v1/orders/${OWN_ORDER}`;
    const IDLE = 'Intercept is off, so requests pass straight through.';
    const EASE_OUT = 'cubic-bezier(0.2, 0.8, 0.2, 1)';

    const state = { phase: 'idle', intercept: false, result: 'none', finding: false };
    let epoch = 0; // bumped by reset and disarm; stale callbacks compare against it
    let lastSpawn = { at: -Infinity, width: 0 };
    let lastBurst = -Infinity;
    let visible = false;
    const timers = new Set();

    function later(ms, fn) {
      const mine = epoch;
      const id = window.setTimeout(() => {
        timers.delete(id);
        if (mine === epoch) fn();
      }, ms);
      timers.add(id);
    }

    function clearWire() {
      timers.forEach((id) => window.clearTimeout(id));
      timers.clear();
      ui.track.querySelectorAll('.tray-chip:not(.tray-chip-static)').forEach((el) => {
        el.getAnimations().forEach((animation) => animation.cancel());
        el.remove();
      });
      ui.suck.classList.remove('is-out');
    }

    // Screen readers hear the sentence; everyone sees the status line.
    function say(text) {
      ui.live.textContent = '';
      window.setTimeout(() => { ui.live.textContent = text; }, 60);
    }

    function show(sentence, tag, tone) {
      ui.status.replaceChildren();
      if (tag) {
        const label = doc.createElement('span');
        label.className = `tray-tag tray-tag-${tone}`;
        label.textContent = tag;
        ui.status.append(label, ' ');
      }
      ui.status.append(sentence);
    }

    function line(text, tone) {
      const el = doc.createElement('span');
      el.className = 'tray-line';
      if (tone) {
        const strong = doc.createElement('span');
        strong.className = tone;
        strong.textContent = text;
        el.append(strong);
      } else {
        el.textContent = text;
      }
      return el;
    }

    function render() {
      tray.dataset.phase = state.phase;
      tray.dataset.intercept = state.intercept ? 'on' : 'off';
      tray.dataset.result = state.result;
      tray.dataset.finding = state.finding ? 'on' : 'off';
      ui.switch.setAttribute('aria-pressed', String(state.intercept));
      ui.switchState.textContent = state.intercept ? 'on' : 'off';
      const held = state.phase === 'held';
      ui.forward.setAttribute('aria-disabled', String(!held));
      ui.drop.setAttribute('aria-disabled', String(!held));
      ui.reset.setAttribute('aria-disabled', String(state.phase === 'idle' && !state.intercept && !state.finding));
      ui.id.readOnly = !held;
      ui.id.tabIndex = held ? 0 : -1;
    }

    // Kirby poses swap in place: idle, inhale (pulling a request in), full (holding it), spit (forwarding it),
    // and ninja: the copy ability he gains when the forwarded request proves the IDOR.
    const poses = {};
    ['idle', 'inhale', 'full', 'spit', 'ninja'].forEach((name) => {
      const src = ui.kirby.dataset[`pose${name[0].toUpperCase()}${name.slice(1)}`];
      if (src) poses[name] = src;
    });
    let pose = 'idle';
    function setPose(name, pop) {
      const next = poses[name];
      if (!next) return;
      if (name !== pose) {
        pose = name;
        ui.kirby.src = next;
      }
      if (pop && !reducedMotion()) replay(ui.kirbyBox, 'is-popping');
    }
    window.setTimeout(() => {
      Object.values(poses).forEach((src) => { const img = new Image(); img.src = src; });
    }, 1200);

    function chip(label, tone) {
      const el = doc.createElement('span');
      el.className = tone ? `tray-chip is-${tone}` : 'tray-chip';
      const space = label.indexOf(' ');
      const method = doc.createElement('b');
      method.textContent = space > 0 ? label.slice(0, space) : label;
      el.append(method, space > 0 ? label.slice(space) : '');
      return el;
    }

    function widthOf(label) {
      const probe = chip(label);
      probe.style.visibility = 'hidden';
      ui.track.append(probe);
      const width = probe.offsetWidth;
      probe.remove();
      return width;
    }

    // Moves a chip along the wire between two x positions; it fades in as it
    // leaves and removes itself on arrival.
    function fly(el, from, to, duration, onArrive) {
      if (!el.isConnected) ui.track.append(el);
      const mine = epoch;
      const animation = el.animate(
        [{ transform: `translateX(${from}px)`, opacity: 0 }, { opacity: 1, offset: 0.06 }, { transform: `translateX(${to}px)`, opacity: 1 }],
        { duration, easing: 'linear', fill: 'forwards' },
      );
      animation.onfinish = () => {
        el.remove();
        if (onArrive && mine === epoch) onArrive();
      };
      return animation;
    }

    // A request leaves the browser, passes Kirby's mouth and drifts to the servers.
    function outbound(el) {
      ui.track.append(el);
      const travel = ui.track.clientWidth;
      lastSpawn = { at: performance.now(), width: el.offsetWidth };
      return fly(el, 0, travel, (travel / SPEED) * 1000);
    }

    // A short burst of shop traffic, under five seconds, so nothing on the page
    // moves for long without being asked to (WCAG 2.2.2).
    function burst() {
      if (reducedMotion() || !visible || state.phase !== 'idle' || state.intercept) return;
      lastBurst = performance.now();
      const next = (index) => {
        if (index >= AMBIENT.length || state.phase !== 'idle' || state.intercept) return;
        outbound(chip(AMBIENT[index]));
        if (index + 1 < AMBIENT.length) {
          later(((widthOf(AMBIENT[index + 1]) + GAP) / SPEED) * 1000, () => next(index + 1));
        }
      };
      next(0);
    }

    function hold() {
      state.phase = 'held';
      state.result = 'none';
      ui.id.value = String(OWN_ORDER);
      sizeId();
      ui.code.textContent = '';
      ui.code.className = 'tray-code';
      ui.wait.textContent = 'Waiting for Forward or Drop.';
      ui.request.classList.remove('is-dropped', 'is-dropping');
      render();
      if (!reducedMotion()) replay(ui.msg, 'is-landing');
      show(`${TARGET}. Change the id, then Forward or Drop.`, 'Held', 'held');
      say(`Held: ${TARGET} on api.example.com. The order id in the path is editable. Try 1043, then choose Forward or Drop.`);
    }

    // Mouth of the inhale pose, as a fraction of Kirby's box, measured on the art.
    const MOUTH = { x: 0.66, y: 0.54 };

    // Kirby inhales: suction lines draw at the mouth and the request shrinks into it; at
    // contact the cheeks-full pose takes over and the request is held.
    function snap(el, flight) {
      const mine = epoch;
      flight.pause();
      setPose('inhale', false);
      const wire = ui.wire.getBoundingClientRect();
      const kirby = ui.kirbyBox.getBoundingClientRect();
      const box = el.getBoundingClientRect();
      const mx = kirby.left + kirby.width * MOUTH.x;
      const my = kirby.top + kirby.height * MOUTH.y;
      ui.suck.style.transform = `translate(${mx - wire.left + 2}px, ${my - wire.top - 15}px)`;
      ui.suck.classList.add('is-out');
      const current = new DOMMatrix(getComputedStyle(el).transform).m41;
      const dx = mx - (box.left + box.width / 2);
      const dy = my - (box.top + box.height / 2);
      el.animate(
        [{ transform: `translateX(${current}px) scale(1)`, opacity: 1 }, { transform: `translate(${current + dx}px, ${dy}px) scale(0.12)`, opacity: 0.2 }],
        { duration: 300, easing: 'cubic-bezier(0.5, 0, 0.9, 0.6)', fill: 'forwards' },
      ).onfinish = () => {
        ui.suck.classList.remove('is-out');
        if (mine !== epoch) {
          // Intercept went off mid-inhale: the request was never held, so it flies on.
          setPose('idle', false);
          if (el.isConnected) flight.play();
          return;
        }
        setPose('full', true);
        flight.cancel();
        el.remove();
        later(220, hold);
      };
    }

    function launch() {
      const el = chip(TARGET);
      const flight = outbound(el);
      const catchAt = Math.min(ui.track.clientWidth * 0.35, 140);
      later((catchAt / SPEED) * 1000, () => snap(el, flight));
    }

    function arm() {
      state.phase = 'armed';
      state.result = 'none';
      render();
      show('Intercept is on. Holding the next request…');
      say('Intercept on. The next request will be held.');
      if (reducedMotion()) {
        setPose('full', false);
        hold();
        return;
      }
      // Let the last passing request clear Kirby's mouth, then the shop sends the next one.
      const clear = ((widthOf(TARGET) + GAP) / SPEED) * 1000 - (performance.now() - lastSpawn.at);
      later(Math.max(200, clear), launch);
    }

    function toggle() {
      state.intercept = !state.intercept;
      if (state.phase === 'busy') {
        // A forwarded request is already on its way; the switch only affects the next one.
        render();
        say(state.intercept ? 'Intercept on.' : 'Intercept off.');
        return;
      }
      if (state.intercept) {
        if (state.phase === 'idle' || state.phase === 'result') {
          arm();
          return;
        }
        render();
        show('Intercept is on.');
        say('Intercept on.');
        return;
      }
      if (state.phase === 'armed') {
        // The request already on its way is not held: it passes straight through.
        epoch += 1;
        timers.forEach((id) => window.clearTimeout(id));
        timers.clear();
        state.phase = 'idle';
        render();
        show(IDLE);
        say(`Intercept off. ${IDLE}`);
        return;
      }
      if (state.phase === 'held') {
        render();
        show('Intercept is off, but this request stays held until you Forward or Drop it.', 'Held', 'held');
        say('Intercept off. The held request still waits for Forward or Drop.');
        return;
      }
      if (state.phase === 'result') {
        state.phase = 'idle';
        state.result = 'none';
        render();
        show(IDLE);
        say(`Intercept off. ${IDLE}`);
        later(300, burst);
        return;
      }
      render();
      show(IDLE);
      say('Intercept off.');
    }

    function customerFor(order) {
      if (order === 1043) return OTHER_CUSTOMER;
      let n = 1000 + ((order * 7919) % 9000);
      if (n === 7731 || n === 8812) n += 1;
      return `cust_${n}`;
    }

    // What the pretend shop answers. Its orders run from 1000 to 1099, and it never
    // checks who is asking: that is the bug the toy lets you find.
    function answer(order) {
      if (order === OWN_ORDER) return { kind: 'own', code: '200 OK', tone: 'ok', customer: OWN_CUSTOMER };
      if (order >= 1000 && order <= 1099) return { kind: 'other', code: '200 OK', tone: 'ok', customer: customerFor(order) };
      return { kind: 'missing', code: '404 Not Found', tone: 'miss' };
    }

    function finish(order, outcome) {
      state.phase = 'result';
      state.result = outcome.kind;
      ui.code.textContent = outcome.code;
      ui.code.className = `tray-code tray-tag tray-tag-${outcome.tone}`;
      const body = outcome.kind === 'missing'
        ? '{"error":"order not found"}'
        : `{"id":${order},"customerId":"${outcome.customer}"}`;
      ui.res.replaceChildren(line(`HTTP/1.1 ${outcome.code}`, outcome.kind === 'missing' ? 'tok-keyword' : 'tok-string'), line(body));
      setPose('idle', false);
      if (outcome.kind === 'own') {
        show('your own order, nothing to report. Reset to try another id.', '200 OK', 'ok');
        say('Forwarded. 200 OK: your own order, nothing to report. Reset to try another id.');
      } else if (outcome.kind === 'other') {
        state.finding = true;
        // Kirby becomes what he eats: sneaking into someone else's order earns him Ninja.
        setPose('ninja', false);
        if (!reducedMotion()) replay(ui.kirbyBox, 'is-copying');
        show(`customer ${outcome.customer}, not you. Kirby copied Ninja.`, '200 OK', 'ok');
        say(`Forwarded. 200 OK: order ${order} belongs to customer ${outcome.customer}, not you. Kirby copied Ninja. Finding filed: Orders API answers any customer (IDOR). Evidence: this flow, attached. The report can go final.`);
        if (!reducedMotion()) replay(ui.finding, 'is-popping');
      } else {
        show(`there is no order ${order}. Reset, then try 1043.`, '404 Not Found', 'miss');
        say(`Forwarded. 404 Not Found: there is no order ${order}. Reset, then try 1043.`);
      }
      render();
    }

    function forward() {
      if (state.phase !== 'held') return;
      const digits = ui.id.value.trim();
      if (!/^\d{1,6}$/.test(digits)) {
        show('Type an order id first, for example 1043.');
        say('Type an order id first, for example 1043.');
        ui.id.focus();
        return;
      }
      const order = Number(digits);
      const outcome = answer(order);
      state.phase = 'busy';
      render();
      ui.wait.textContent = 'Waiting for the shop…';
      show(`Forwarding GET /api/v1/orders/${digits}…`);
      if (reducedMotion()) {
        finish(order, outcome);
        return;
      }
      const travel = ui.track.clientWidth;
      setPose('spit', true);
      const star = chip(`GET /api/v1/orders/${digits}`, 'target');
      star.classList.add('is-star');
      fly(star, 0, travel, LEG, () => {
        const reply = chip(outcome.code, outcome.tone);
        ui.track.append(reply);
        fly(reply, travel, -reply.offsetWidth, LEG, () => finish(order, outcome));
      });
    }

    function drop() {
      if (state.phase !== 'held') return;
      state.phase = 'result';
      state.result = 'dropped';
      ui.code.textContent = 'Dropped';
      ui.code.className = 'tray-code tray-tag tray-tag-drop';
      ui.res.replaceChildren(line('No response. The request never left the proxy.'), line(' '));
      ui.request.classList.add('is-dropped');
      setPose('idle', true); // Kirby swallows it
      if (!reducedMotion()) replay(ui.request, 'is-dropping');
      render();
      show('The app never saw it. Reset to play again.', 'Dropped', 'drop');
      say('Dropped. The app never saw it. Reset to play again.');
    }

    function reset() {
      epoch += 1;
      clearWire();
      state.phase = 'idle';
      state.intercept = false;
      state.result = 'none';
      state.finding = false;
      ui.id.value = String(OWN_ORDER);
      sizeId();
      ui.code.textContent = '';
      ui.code.className = 'tray-code';
      ui.request.classList.remove('is-dropped', 'is-dropping');
      ui.finding.classList.remove('is-popping');
      setPose('idle', false);
      render();
      show(IDLE);
      say(`Reset. ${IDLE}`);
      later(450, burst);
    }

    function sizeId() {
      ui.id.style.setProperty('--chars', String(Math.max(4, ui.id.value.length)));
    }

    ui.id.addEventListener('input', () => {
      const clean = ui.id.value.replace(/\D+/g, '').slice(0, 6);
      if (clean !== ui.id.value) ui.id.value = clean;
      sizeId();
    });
    ui.id.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        forward();
      }
    });
    // An action can disable the button that ran it; focus then moves to the control
    // that comes next instead of staying on a dead button.
    const act = (fn, next) => (event) => {
      const button = event.currentTarget;
      if (button.getAttribute('aria-disabled') === 'true') return;
      fn();
      if (doc.activeElement === button && button.getAttribute('aria-disabled') === 'true') next().focus();
    };
    ui.switch.addEventListener('click', toggle);
    ui.forward.addEventListener('click', act(forward, () => ui.reset));
    ui.drop.addEventListener('click', act(drop, () => ui.reset));
    ui.reset.addEventListener('click', act(reset, () => ui.switch));

    // Traffic only runs while the tray is on screen, once per visit to it.
    if ('IntersectionObserver' in window) {
      new IntersectionObserver((entries) => {
        entries.forEach((entry) => {
          visible = entry.isIntersecting && entry.intersectionRatio >= 0.35;
          if (visible && performance.now() - lastBurst > 15000) later(500, burst);
        });
      }, { threshold: [0, 0.35] }).observe(tray);
    }

    sizeId();
    render();
  }

  /* ------------------------------------------------------------------------
     The real recording: sources follow the theme, playback starts only in view
     and only when motion is allowed, a visible button pauses it (WCAG 2.2.2),
     and the chapter list doubles as the text description and as a seek bar.
     ------------------------------------------------------------------------ */
  function initDemo(video) {
    if (!video) return;
    const frame = video.closest('.demo-window');
    const controls = frame && frame.querySelector('[data-demo-controls]');
    const toggle = controls && controls.querySelector('[data-demo-toggle]');
    const label = toggle && toggle.querySelector('[data-demo-toggle-label]');
    const bar = controls && controls.querySelector('[data-demo-progress]');
    const clock = controls && controls.querySelector('[data-demo-clock]');
    const list = doc.querySelector('[data-demo-chapters]');
    const sources = [...video.querySelectorAll('source')];
    if (!toggle || !label || sources.length !== 2) return;

    let userPaused = false;
    let started = false;
    let frame0 = 0;
    const minutes = (seconds) => {
      const whole = Math.max(0, Math.floor(seconds));
      return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, '0')}`;
    };

    video.removeAttribute('controls');
    video.classList.add('is-scripted');
    controls.hidden = false;

    function sync() {
      const playing = !video.paused;
      toggle.dataset.state = playing ? 'playing' : 'paused';
      label.textContent = playing ? 'Pause' : 'Play';
    }

    function play() {
      const attempt = video.play();
      if (attempt && typeof attempt.catch === 'function') attempt.catch(sync);
    }

    function progress() {
      window.cancelAnimationFrame(frame0);
      const step = () => {
        const duration = video.duration || 34.9;
        if (bar) bar.style.transform = `scaleX(${Math.min(1, video.currentTime / duration)})`;
        if (clock) clock.textContent = `${minutes(video.currentTime)} / ${minutes(Math.round(duration))}`;
        if (!video.paused) frame0 = window.requestAnimationFrame(step);
      };
      step();
    }

    function useTheme(theme) {
      const name = theme === 'dark' ? 'Dark' : 'Light';
      const poster = video.dataset[`poster${name}`];
      const webm = video.dataset[`webm${name}`];
      const mp4 = video.dataset[`mp4${name}`];
      if (!poster || !webm || !mp4) return;
      const time = video.currentTime;
      const playing = !video.paused;
      video.poster = poster;
      sources[0].src = webm;
      sources[1].src = mp4;
      // load() re-runs source selection. Before the first play nothing downloads
      // (preload="none"); after it, load the new theme and carry on where it was.
      if (started) {
        video.preload = 'auto';
        video.addEventListener('loadedmetadata', () => {
          try { video.currentTime = time; } catch (_) { /* keep the start */ }
          if (playing) play();
        }, { once: true });
      }
      video.load();
      sync();
    }

    // Chapters become buttons that seek; the current beat is marked as aria-current="time".
    const chapters = list ? [...list.querySelectorAll('li[data-start]')].map((item) => {
      const button = doc.createElement('button');
      button.type = 'button';
      button.className = 'demo-chapter';
      button.append(...item.childNodes);
      item.append(button);
      const start = Number(item.dataset.start) || 0;
      button.addEventListener('click', () => {
        userPaused = false;
        const seek = () => { video.currentTime = start + 0.05; };
        if (video.readyState >= 1) seek();
        else video.addEventListener('loadedmetadata', seek, { once: true });
        play();
      });
      return { start, button };
    }) : [];
    if (list) list.classList.add('is-live');

    let current = -2;
    function mark() {
      let index = -1;
      if (started) chapters.forEach((chapter, i) => { if (video.currentTime >= chapter.start) index = i; });
      if (index === current) return;
      current = index;
      chapters.forEach((chapter, i) => {
        if (i === index) chapter.button.setAttribute('aria-current', 'time');
        else chapter.button.removeAttribute('aria-current');
      });
    }

    video.addEventListener('play', () => { started = true; sync(); progress(); mark(); });
    video.addEventListener('pause', () => { sync(); progress(); });
    video.addEventListener('timeupdate', mark);
    video.addEventListener('seeked', () => { mark(); progress(); });
    video.addEventListener('click', () => toggle.click());
    toggle.addEventListener('click', () => {
      if (video.paused) {
        userPaused = false;
        play();
      } else {
        userPaused = true;
        video.pause();
      }
    });

    if ('IntersectionObserver' in window) {
      new IntersectionObserver((entries) => {
        entries.forEach((entry) => {
          const inView = entry.isIntersecting && entry.intersectionRatio >= 0.5;
          if (inView && !userPaused && !reducedMotion() && video.paused) play();
          if (!inView && !video.paused) video.pause();
        });
      }, { threshold: [0, 0.5] }).observe(video);
    }
    motionQuery.addEventListener('change', () => {
      if (reducedMotion() && !video.paused) video.pause();
    });

    if (currentTheme() === 'dark') useTheme('dark');
    watchTheme(useTheme);
    sync();
  }

  /* ------------------------------------------------------------------------
     The stage map: the five stages become a tablist (APG tabs, automatic
     activation). Without JavaScript they stay an ordered list of anchors to
     panels that are all shown in order.
     ------------------------------------------------------------------------ */
  function initStages(container) {
    if (!container) return;
    const list = container.querySelector('[data-stage-list]');
    const tabs = [...container.querySelectorAll('[data-stage-tab]')];
    const panels = tabs.map((tab) => doc.getElementById((tab.getAttribute('href') || '').slice(1)));
    if (!list || !tabs.length || panels.some((panel) => !panel)) return;
    const vertical = window.matchMedia('(max-width: 639px)');

    function select(index, focus) {
      tabs.forEach((tab, i) => {
        const on = i === index;
        tab.setAttribute('aria-selected', String(on));
        tab.tabIndex = on ? 0 : -1;
        panels[i].hidden = !on;
      });
      if (focus) tabs[index].focus();
    }

    list.setAttribute('role', 'tablist');
    [...list.children].forEach((item) => item.setAttribute('role', 'presentation'));
    tabs.forEach((tab, i) => {
      tab.setAttribute('role', 'tab');
      tab.setAttribute('aria-controls', panels[i].id);
      panels[i].setAttribute('role', 'tabpanel');
      panels[i].setAttribute('aria-labelledby', tab.id);
      panels[i].tabIndex = 0;
      tab.addEventListener('click', (event) => {
        event.preventDefault();
        select(i, false);
      });
      tab.addEventListener('keydown', (event) => {
        const last = tabs.length - 1;
        const nextKey = vertical.matches ? 'ArrowDown' : 'ArrowRight';
        const prevKey = vertical.matches ? 'ArrowUp' : 'ArrowLeft';
        let next = -1;
        if (event.key === nextKey) next = i === last ? 0 : i + 1;
        else if (event.key === prevKey) next = i === 0 ? last : i - 1;
        else if (event.key === 'Home') next = 0;
        else if (event.key === 'End') next = last;
        else if (event.key === ' ') next = i;
        if (next < 0) return;
        event.preventDefault();
        select(next, true);
      });
    });
    const orient = () => list.setAttribute('aria-orientation', vertical.matches ? 'vertical' : 'horizontal');
    orient();
    vertical.addEventListener('change', orient);
    container.classList.add('is-tabs');

    const fromHash = () => panels.findIndex((panel) => `#${panel.id}` === window.location.hash);
    select(Math.max(0, fromHash()), false);
    window.addEventListener('hashchange', () => {
      const index = fromHash();
      if (index >= 0) select(index, false);
    });
  }

  initTray(doc.querySelector('[data-tray]'));
  initDemo(doc.querySelector('[data-demo-video]'));
  initStages(doc.querySelector('[data-stages]'));
})();
