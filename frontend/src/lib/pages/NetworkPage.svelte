<script lang="ts">
  import { onDestroy } from 'svelte'
  import { RefreshCw, LoaderCircle, ChevronLeft } from '@lucide/svelte'
  import { api } from '../api'
  import { app } from '../state.svelte'
  import { forwardingProblem, splitForwarded } from '../network'
  import { t, tSplit } from '../i18n.svelte'

  // States are told by tinting the value text (DESIGN.md); only the verdict
  // carries a dot, so a healthy page isn't a column of identical green dots.
  // ok: working · info (sea): still finding out · warn: limits what works ·
  // bad: blocks transfers · muted: off or unknown, nothing to act on.
  type Tone = 'ok' | 'info' | 'warn' | 'bad' | 'muted'
  const tint: Record<Tone, string> = {
    ok: 'text-ok',
    info: 'text-sea',
    warn: 'text-warn',
    bad: 'text-bad',
    muted: 'text-muted',
  }
  const dot: Record<Tone, string> = { ok: 'bg-ok', info: 'bg-sea', warn: 'bg-warn', bad: 'bg-bad', muted: 'bg-muted/50' }

  const net = $derived(app.network)
  const online = $derived(net.serverOnline)
  // Offline without an error yet means the first connection is still underway.
  const connecting = $derived(!online && !net.serverError)
  // A strict router with no forward depends on the relay and on forwarding.
  const strictUnforwarded = $derived(net.router === 'strict' && !net.forwarded)
  const listenPort = $derived(app.device?.port || 7778)

  // Freshness: "Checked just now" / "Checked 4 min ago".
  let now = $state(Date.now())
  const tick = setInterval(() => (now = Date.now()), 15_000)
  onDestroy(() => clearInterval(tick))
  const checkedText = $derived.by(() => {
    if (!app.networkCheckedAt) return t('network.checkedAuto')
    const mins = Math.floor((now - app.networkCheckedAt) / 60_000)
    return mins < 1 ? t('network.checkedNow') : t('network.checkedAgo', { count: mins })
  })

  function openListeningPort() {
    app.settingsFocus = 'listen-port'
    app.page = 'settings'
  }

  // One sentence that answers "can my friends reach me?".
  const verdict = $derived.by((): { tone: Tone; title: string; body: string } => {
    const v = (tone: Tone, title: string, body: string, params = {}) => ({ tone, title: t(title), body: t(body, params) })
    if (connecting) return v('info', 'network.vConnectingTitle', 'network.vWaitBody')
    if (!online) return v('bad', 'network.vOfflineTitle', 'network.vOfflineBody')
    if (net.checking && net.router === 'unknown') return v('info', 'network.vCheckingTitle', 'network.vWaitBody')
    if (net.forwarded || net.router === 'open') return v('ok', 'network.vDirectTitle', 'network.vDirectBody')
    if (net.router === 'restricted') return v('ok', 'network.vMostTitle', 'network.vMostBody')
    if (net.router === 'strict') {
      return net.relay
        ? v('warn', 'network.vRelayTitle', 'network.vRelayBody', { port: listenPort })
        : v('bad', 'network.vBlockedTitle', 'network.vBlockedBody', { port: listenPort })
    }
    return v('muted', 'network.vConnectedTitle', net.detection ? 'network.vSoonBody' : 'network.vNoDetectBody')
  })

  const routerRow = $derived.by((): { tone: Tone; value: string; note: string; term?: string } => {
    const stale = !online && net.router !== 'unknown'
    const note = (key: string) => t(key) + (stale ? ' ' + t('network.lastKnown') : '')
    switch (net.router) {
      case 'open':
        return { tone: stale ? 'muted' : 'ok', value: t('network.open'), note: note('network.openNote') }
      case 'restricted':
        return { tone: stale ? 'muted' : 'ok', value: t('network.restricted'), note: note('network.restrictedNote') }
      case 'strict':
        return {
          tone: stale ? 'muted' : net.forwarded ? 'ok' : 'warn',
          value: t('network.strict'),
          term: t('network.strictTerm'),
          note: note('network.strictNote'),
        }
      default:
        if (online && net.checking) return { tone: 'info', value: t('network.checking'), note: t('network.checkingRouter') }
        return {
          tone: 'muted',
          value: t('network.unknown'),
          note: t(online && !net.detection ? 'network.noDetect' : 'network.needsServer'),
        }
    }
  })

  const forwarding = $derived.by((): { tone: Tone; value: string; note: string; addr: string; term?: string } => {
    if (!net.portMapping) return { tone: 'muted', value: t('network.fOff'), note: t('network.fOffNote'), addr: '' }
    if (net.forwarded) {
      const { method, addr } = splitForwarded(net.forwarded)
      return { tone: 'ok', value: t('network.fActive'), note: '', addr, term: t('network.fVia', { method }) }
    }
    if (net.mappingErr) {
      // Only a problem worth colouring when the router needs it.
      const tone: Tone = strictUnforwarded && online ? 'warn' : 'muted'
      return { tone, value: t('network.fNotAvailable'), note: forwardingProblem(net.mappingErr), addr: '' }
    }
    return { tone: 'info', value: t('network.fTrying'), note: t('network.fAsking'), addr: '' }
  })
  const [activeBefore, activeAfter] = $derived(tSplit('network.fActiveNote', 'addr'))
  // When automatic forwarding can't help and the router is strict, point at
  // the setting that matters for forwarding by hand.
  const manualForwardingHelps = $derived(strictUnforwarded)

  // Without a relay, a strict router can't receive from other networks at all.
  const relayTone = $derived<Tone>(!online ? 'muted' : net.relay ? 'ok' : strictUnforwarded ? 'bad' : 'warn')
</script>

<div class="page">
  <header class="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
    <div>
      <!-- Phone: reached from Settings (the tab bar has no Network), so lead back there. -->
      <button class="btn-quiet mb-1 -ml-2.5 sm:hidden" onclick={() => (app.page = 'settings')}>
        <ChevronLeft size={18} /> {t('network.back')}
      </button>
      <h1 class="page-title">{t('network.title')}</h1>
      <p class="mt-1 text-muted">{t('network.subtitle', { checked: checkedText })}</p>
    </div>
    <button class="btn-secondary whitespace-nowrap" disabled={net.checking} onclick={() => api.recheckNetwork()}>
      {#if net.checking}
        <LoaderCircle size={16} class="animate-spin" /> {t('network.checking')}
      {:else}
        <RefreshCw size={16} /> {t('network.checkAgain')}
      {/if}
    </button>
  </header>

  <section class="flex items-start gap-3" aria-live="polite">
    <span class="mt-2 size-2.5 shrink-0 rounded-full {dot[verdict.tone]}" aria-hidden="true"></span>
    <div>
      <h2 class="text-xl leading-snug font-medium {verdict.tone === 'muted' ? '' : tint[verdict.tone]}">{verdict.title}</h2>
      <p class="mt-1 max-w-[60ch] text-muted">{verdict.body}</p>
    </div>
  </section>

  <div class="flex flex-col">
    <section class="row">
      <div>
        <h2 class="section-title">{t('network.server')}</h2>
        <p class="hint">{t('network.serverHint')}</p>
      </div>
      <div>
        <div class="font-medium {tint[online ? 'ok' : connecting ? 'info' : 'bad']}">
          {online
            ? net.rttMs > 0
              ? t('network.connectedMs', { ms: net.rttMs })
              : t('network.connected')
            : t(connecting ? 'network.connecting' : 'network.unreachable')}
        </div>
        <div class="selectable mt-0.5 font-mono text-sm break-all text-muted">{net.serverAddr}</div>
        {#if !online && net.serverError}
          <p class="mt-1 max-w-[48ch] text-sm text-muted">{net.serverError}</p>
        {/if}
      </div>
    </section>

    <section class="row">
      <div>
        <h2 class="section-title">{t('network.publicAddr')}</h2>
        <p class="hint">{t('network.publicHint')}</p>
      </div>
      <div>
        <div class="selectable font-mono text-sm break-all {net.publicAddr && online ? 'text-ink' : 'text-muted'}">
          {net.publicAddr || t('network.notKnown')}
        </div>
        {#if net.publicAddr && !online}
          <p class="mt-0.5 text-sm text-muted">{t('network.lastKnown')}</p>
        {/if}
      </div>
    </section>

    <section class="row">
      <div>
        <h2 class="section-title">{t('network.router')}</h2>
        <p class="hint">{t('network.routerHint')}</p>
      </div>
      <div>
        <div class="font-medium {tint[routerRow.tone]}" title={routerRow.term}>{routerRow.value}</div>
        <p class="mt-0.5 max-w-[48ch] text-sm text-muted">{routerRow.note}</p>
      </div>
    </section>

    <section class="row">
      <div>
        <h2 class="section-title">{t('network.forwarding')}</h2>
        <p class="hint">{t('network.forwardingHint')}</p>
      </div>
      <div>
        <div class="font-medium {tint[forwarding.tone]}" title={forwarding.term ?? t('network.fViaDefault')}>{forwarding.value}</div>
        <p class="mt-0.5 max-w-[48ch] text-sm text-muted">
          {#if forwarding.addr}
            {activeBefore}<span class="selectable font-mono text-ink">{forwarding.addr}</span>{activeAfter}
          {:else}
            {forwarding.note}
          {/if}
        </p>
        {#if manualForwardingHelps}
          <button class="mt-1 text-sm font-medium text-primary underline decoration-primary/40 underline-offset-4 hover:decoration-primary pointer-coarse:py-2" onclick={openListeningPort}>
            {t('network.fByHand', { port: listenPort })}
          </button>
        {:else}
          <button class="mt-1 text-sm font-medium text-primary underline decoration-primary/40 underline-offset-4 hover:decoration-primary pointer-coarse:py-2" onclick={() => (app.page = 'settings')}>
            {t('network.fChange')}
          </button>
        {/if}
      </div>
    </section>

    <section class="row">
      <div>
        <h2 class="section-title">{t('network.relay')}</h2>
        <p class="hint">{t('network.relayHint')}</p>
      </div>
      <div>
        <div class="font-medium {tint[relayTone]}">
          {t(!online ? 'network.unknown' : net.relay ? 'network.relayAvailable' : 'network.relayOff')}
        </div>
        <p class="mt-0.5 max-w-[48ch] text-sm text-muted">
          {t(!online ? 'network.relayUnknownNote' : net.relay ? 'network.relayAvailableNote' : 'network.relayOffNote')}
        </p>
      </div>
    </section>
  </div>
</div>

<style>
  .row {
    display: grid;
    grid-template-columns: minmax(0, 18rem) minmax(0, 1fr);
    align-items: start;
    gap: 2.5rem;
    padding-block: 1.5rem;
    border-top: 1px solid var(--line);
  }
  .hint {
    margin-top: 0.25rem;
    font-size: 0.875rem;
    color: var(--muted);
    max-width: 40ch;
  }
  /* Narrow windows: label above value instead of side by side. */
  @media (max-width: 1000px) {
    .row {
      grid-template-columns: minmax(0, 1fr);
      gap: 0.75rem;
    }
  }
</style>
