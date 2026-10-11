<script lang="ts">
  // Phones: what to send, before any picker opens. Photos and videos come
  // from the system photo picker, which has no camera; files and folders
  // from the document picker.
  import { Images, FileUp, FolderUp } from '@lucide/svelte'
  import Dialog from './Dialog.svelte'
  import type { Peer } from '../api'
  import { pickAndSend, type PickKind } from '../state.svelte'
  import { t } from '../i18n.svelte'

  let { peer, onclose }: { peer: Peer; onclose: () => void } = $props()

  const choices: { kind: PickKind; icon: typeof Images; label: string }[] = $derived([
    { kind: 'media', icon: Images, label: t('peer.pickPhotos') },
    { kind: 'files', icon: FileUp, label: t('peer.pickFiles') },
    { kind: 'folder', icon: FolderUp, label: t('peer.pickFolder') },
  ])

  function choose(kind: PickKind) {
    onclose()
    pickAndSend(peer, kind)
  }
</script>

<Dialog title={t('peer.chooseTitle', { name: peer.name })} {onclose}>
  <div class="grid grid-cols-3 gap-3">
    {#each choices as c (c.kind)}
      <button
        class="flex flex-col items-center gap-3 rounded-lg bg-surface-2 px-2 pt-5 pb-4 text-sm
          transition-[background-color,scale] duration-160 ease-out-expo active:scale-[0.97] active:bg-line"
        onclick={() => choose(c.kind)}
      >
        <span class="grid size-12 place-items-center rounded-full bg-surface text-primary">
          <c.icon size={22} strokeWidth={1.75} />
        </span>
        {c.label}
      </button>
    {/each}
  </div>
</Dialog>
