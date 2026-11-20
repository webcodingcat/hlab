<script lang="ts">
  import type { Page } from "../../state";
  import ImageList from "../images/ImageList.svelte";

  // The Snapshots section of a project console: the project's snapshots.
  let { project, page }: { project: string; page: Page } = $props();
</script>

<ImageList {project} {page} type="snapshot" />
