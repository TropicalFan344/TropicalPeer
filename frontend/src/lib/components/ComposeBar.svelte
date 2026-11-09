<script lang="ts">
  // A computer's peer card, opened up (💬, right-click or a paste): a few
  // words that go on their own (➤ or Enter), or with files chosen with the
  // paperclip or dropped onto the card.
  import { slide } from 'svelte/transition'
  import { Paperclip, SendHorizontal } from '@lucide/svelte'
  import { maxNoteBytes, maxTextBytes, textBytes, type Peer } from '../api'
  import { app, closeCompose, pickAndSend, sendText } from '../state.svelte'
  import { formatSize } from '../format'
  import { dur, easeOutExpo } from '../motion'
  import { t } from '../i18n.svelte'

  let { peer }: { peer: Peer } = $props()

  let sending = $state(false)
  let error = $state('')
  let field = $state<HTMLTextAreaElement>()

  const body = $derived(app.compose.body)
  const empty = $derived(body.trim() === '')
  const bytes = $derived(textBytes(body))
  const tooLongText = $derived(bytes > maxTextBytes)
  const tooLongNote = $derived(bytes > maxNoteBytes)

  async function send() {
    if (empty || tooLongText || sending) return
    sending = true
    error = await sendText(peer, body)
    sending = false
    if (!error) closeCompose()
  }

  async function attach() {
    if (tooLongNote || sending) return
    // Sending closes the bar; a cancelled picker leaves it as it was.
    await pickAndSend(peer, 'files', empty ? '' : body)
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation()
      closeCompose()
    } else if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      send()
    }
  }

  // Grows with the text (and its placeholder, which wraps on a narrow
  // card), up to about six lines, then scrolls.
  function grow(node = field) {
    if (!node) return
    node.style.height = 'auto'
    const border = node.offsetHeight - node.clientHeight
    node.style.height = `${Math.min(node.scrollHeight + border, 160)}px`
  }
  $effect(() => {
    void body
    grow()
  })

  // The slide measures the bar's height as it starts, so the field takes
  // its height first (or the card jumps when the slide ends).
  const sized = (node: HTMLTextAreaElement) => grow(node)

  // Focused once the slide is done: the slide clips what it holds, focus
  // ring included.
  let focused = false
  function focus() {
    if (focused || !field) return
    focused = true
    field.focus()
    field.setSelectionRange(field.value.length, field.value.length)
  }
  $effect(() => {
    const t = setTimeout(focus, dur(200) + 50) // in case the slide never reports its end
    return () => clearTimeout(t)
  })
</script>

<!-- Above the card's own click target: clicks here never open the file picker. -->
<div
  class="relative z-10 -mt-1 px-4 pt-1 pb-4"
  data-no-drag
  transition:slide={{ duration: dur(200), easing: easeOutExpo }}
  onintroend={focus}
>
  <div class="flex items-end gap-2">
    <textarea
      bind:this={field}
      class="field min-h-10 flex-1 resize-none py-2 leading-snug"
      rows="1"
      placeholder={t('text.placeholder')}
      aria-label={t('text.compose', { name: peer.name })}
      aria-invalid={tooLongText || !!error}
      bind:value={app.compose.body}
      oninput={() => (error = '')}
      {onkeydown}
      use:sized
    ></textarea>
    <button
      class="btn-quiet size-10 shrink-0 justify-center p-0"
      title={t('text.attach')}
      aria-label={t('text.attach')}
      disabled={tooLongNote || sending}
      onclick={attach}
    >
      <Paperclip size={18} />
    </button>
    <button
      class="btn-primary size-10 shrink-0 justify-center p-0"
      title={t('text.sendText')}
      aria-label={t('text.sendText')}
      disabled={empty || tooLongText || sending}
      onclick={send}
    >
      <SendHorizontal size={18} />
    </button>
  </div>
  <!-- Quiet until there's something to say: the drop hint only once there
       are words to go with files (the card says it again while files hover). -->
  {#if error || tooLongText || sending || tooLongNote || !empty}
    <p class="mt-1.5 text-sm {error || tooLongText ? 'text-bad' : 'text-muted'}" role={error ? 'alert' : undefined}>
      {#if error}
        {error}
      {:else if tooLongText}
        {t('text.tooLong')}
      {:else if sending}
        {t('text.sending')}
      {:else if tooLongNote}
        {t('text.noteTooLong')} ({formatSize(bytes)} / {formatSize(maxNoteBytes)})
      {:else}
        {t('text.attachHintDesktop')}
      {/if}
    </p>
  {/if}
</div>
