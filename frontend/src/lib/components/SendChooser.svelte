<script lang="ts">
  // Phones: what to send, before any picker opens. Photos and videos come
  // from the system photo picker, which has no camera; files and folders
  // from the document picker. Words typed below go on their own (➤), or
  // with whatever is chosen above.
  import { Images, FileUp, FolderUp, SendHorizontal } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import { maxNoteBytes, maxTextBytes, textBytes, type Peer } from '../api'
  import { pickAndSend, sendText, type PickKind } from '../state.svelte'
  import { formatSize } from '../format'
  import { t } from '../i18n.svelte'

  // focusText: opened to write (the sheet's "Send text"), so the keyboard comes up.
  let { peer, onclose, focusText = false }: { peer: Peer; onclose: () => void; focusText?: boolean } = $props()

  const choices: { kind: PickKind; icon: typeof Images; label: string }[] = $derived([
    { kind: 'media', icon: Images, label: t('peer.pickPhotos') },
    { kind: 'files', icon: FileUp, label: t('peer.pickFiles') },
    { kind: 'folder', icon: FolderUp, label: t('peer.pickFolder') },
  ])

  let body = $state('')
  let sending = $state(false)
  let error = $state('')
  const empty = $derived(body.trim() === '')
  const bytes = $derived(textBytes(body))
  const tooLongText = $derived(bytes > maxTextBytes)
  const tooLongNote = $derived(bytes > maxNoteBytes)

  async function choose(kind: PickKind) {
    if (!empty && tooLongNote) return
    const note = empty ? '' : body
    // Keep the sheet (and the words) if the picker is cancelled.
    if (await pickAndSend(peer, kind, note)) onclose()
  }

  async function send() {
    if (empty || tooLongText || sending) return
    sending = true
    error = await sendText(peer, body)
    sending = false
    if (!error) onclose()
  }

  const focus = (node: HTMLTextAreaElement) => {
    if (focusText) node.focus()
  }
</script>

<Dialog title={t('peer.chooseTitle', { name: peer.name })} {onclose}>
  <div class="grid grid-cols-3 gap-3">
    {#each choices as c (c.kind)}
      <button
        class="flex flex-col items-center gap-3 rounded-lg bg-surface-2 px-2 pt-5 pb-4 text-sm
          transition-[background-color,scale] duration-160 ease-out-expo active:scale-[0.97] active:bg-line disabled:opacity-50"
        disabled={!empty && tooLongNote}
        onclick={() => choose(c.kind)}
      >
        <span class="grid size-12 place-items-center rounded-full bg-surface text-primary">
          <c.icon size={22} strokeWidth={1.75} />
        </span>
        {c.label}
      </button>
    {/each}
  </div>

  <div class="mt-4 flex items-end gap-2">
    <textarea
      class="field max-h-40 min-h-12 flex-1 resize-none py-3 leading-snug [field-sizing:content]"
      rows="1"
      placeholder={t('text.placeholder')}
      aria-label={t('text.compose', { name: peer.name })}
      aria-invalid={tooLongText || !!error}
      bind:value={body}
      oninput={() => (error = '')}
      use:focus
    ></textarea>
    <button
      class="btn-primary size-12 shrink-0 justify-center p-0"
      aria-label={t('text.sendText')}
      disabled={empty || tooLongText || sending}
      onclick={send}
    >
      <SendHorizontal size={20} />
    </button>
  </div>
  {#if error || tooLongText || tooLongNote || !empty || sending}
    <p class="mt-2 text-sm {error || tooLongText ? 'text-bad' : 'text-muted'}" role={error ? 'alert' : undefined}>
      {#if error}
        {error}
      {:else if tooLongText}
        {t('text.tooLong')}
      {:else if sending}
        {t('text.sending')}
      {:else if tooLongNote}
        {t('text.noteTooLong')} ({formatSize(bytes)} / {formatSize(maxNoteBytes)})
      {:else}
        {t('text.attachHint')}
      {/if}
    </p>
  {/if}
</Dialog>
