<script lang="ts">
  import type { Page } from "../../state";
  import ImageList from "../images/ImageList.svelte";

  // The Backups section of a project console: the project's backups.
  let { project, page }: { project: string; page: Page } = $props();
</script>

<ImageList {project} {page} type="backup" />
