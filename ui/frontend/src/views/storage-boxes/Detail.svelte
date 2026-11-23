<script lang="ts">
  import { hetznerApi } from "../../lib/api";
  import type { StorageBox } from "../../lib/resources/storageBoxes";
  import { showError } from "../../lib/toast";

  // The detail page of one storage box; for now it shows the box's name
  // (tab is taken but not yet shown).
  let { project, id, list }: { project: string; id: number; tab: string; list: string } = $props();

  let box = $state<StorageBox | undefined>();

  $effect(() => {
    hetznerApi<{ storage_box: StorageBox }>(project, "GET", `/storage_boxes/${id}`)
      .then((r) => (box = r.storage_box))
      .catch((err) => showError("Load the storage box", err));
  });
</script>

<p><a href={list}>Storage Boxes</a></p>
<h1>{box?.name ?? ""}</h1>
