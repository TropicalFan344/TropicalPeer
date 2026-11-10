<script lang="ts" module>
  // Open dialogs, newest last. Only the top one answers Escape and keeps
  // keyboard focus, so a confirm opened from a dialog closes on its own.
  const open: symbol[] = []
</script>

<script lang="ts">
  import { onMount, tick, type Snippet } from 'svelte'
  import { fade, fly, scale } from 'svelte/transition'
  import { X } from '@lucide/svelte'
  import { closeOnBack } from '../back'
  import { dur, easeOutExpo } from '../motion'
  import { t } from '../i18n.svelte'

  let {
    title,
    onclose,
    children,
    width = 'max-w-md',
  }: { title: string; onclose: () => void; children: Snippet; width?: string } = $props()

  const me = Symbol()
  let panel: HTMLElement | undefined = $state()
  // Where focus goes back to when the dialog closes: the button that opened it.
  const opener = document.activeElement as HTMLElement | null

  onMount(() => {
    open.push(me)
    // A field inside may have taken focus already; otherwise the panel takes
    // it, so a screen reader announces the dialog and Tab starts inside it.
    tick().then(() => {
      if (panel && !panel.contains(document.activeElement)) panel.focus()
    })
    return () => {
      open.splice(open.indexOf(me), 1)
      if (opener?.isConnected) opener.focus()
    }
  })

  const focusable = () =>
    [...(panel?.querySelectorAll<HTMLElement>('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])') ?? [])].filter(
      (el) => !(el as HTMLButtonElement).disabled && el.offsetParent !== null,
    )

  function onkeydown(e: KeyboardEvent) {
    if (open.at(-1) !== me) return
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose()
    } else if (e.key === 'Tab' && panel) {
      // Tab stays inside the dialog: past the last control it wraps to the first.
      const els = focusable()
      if (!els.length) return e.preventDefault()
      const first = els[0]!
      const last = els[els.length - 1]!
      const at = document.activeElement
      const inside = panel.contains(at)
      if (e.shiftKey && (!inside || at === first || at === panel)) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && (!inside || at === last)) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  // Android's back button closes the sheet before anything else.
  $effect(() => closeOnBack(() => onclose()))

  // On a phone the dialog is a sheet rising from the bottom edge, in reach of
  // the thumb; on desktop it scales in at the centre.
  const phone = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 639.98px)').matches
  const enter = (node: Element) =>
    phone ? fly(node, { y: 48, duration: dur(240), easing: easeOutExpo }) : scale(node, { start: 0.96, duration: dur(160) })
</script>

<svelte:window {onkeydown} />

<div class="fixed inset-0 z-40 grid place-items-center p-6 max-sm:place-items-end max-sm:p-0">
  <button
    class="absolute inset-0 cursor-default bg-scrim backdrop-blur-[2px]"
    aria-label={t('common.closeDialog')}
    tabindex="-1"
    onclick={onclose}
    transition:fade={{ duration: 150 }}
  ></button>
  <div
    class="relative flex max-h-[calc(100vh-3rem)] w-full {width} flex-col rounded-lg border border-line bg-surface shadow-float outline-none
      max-sm:max-h-[calc(100dvh-var(--sat)-1.5rem)] max-sm:max-w-none max-sm:rounded-b-none max-sm:border-x-0 max-sm:border-b-0"
    bind:this={panel}
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    aria-label={title}
    transition:enter
  >
    <!-- The grip says "this is a sheet" before anyone reads it. -->
    <div class="mx-auto mt-2 h-1 w-9 rounded-full bg-line sm:hidden" aria-hidden="true"></div>
    <header class="flex shrink-0 items-center justify-between px-6 pt-5 max-sm:px-5 max-sm:pt-3">
      <h2 class="text-lg font-medium">{title}</h2>
      <button
        class="rounded-md p-1 text-muted hover:bg-surface-2 hover:text-ink max-sm:-mr-3 max-sm:p-[13px]"
        aria-label={t('common.close')}
        onclick={onclose}
      >
        <X size={18} />
      </button>
    </header>
    <div class="min-h-0 overflow-y-auto px-6 pt-4 pb-6 max-sm:px-5 max-sm:pb-[calc(var(--sab)+1.5rem)]">
      {@render children()}
    </div>
  </div>
</div>
