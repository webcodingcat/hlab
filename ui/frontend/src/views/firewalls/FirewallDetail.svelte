<script lang="ts">
  import { untrack } from "svelte";
  import Button from "../../lib/Button.svelte";
  import ConfirmDialog from "../../lib/ConfirmDialog.svelte";
  import DetailPage from "../../lib/DetailPage.svelte";
  import Dialog from "../../lib/Dialog.svelte";
  import Field from "../../lib/Field.svelte";
  import LabelsTab from "../../lib/LabelsTab.svelte";
  import ResourceActionsMenu from "../../lib/ResourceActionsMenu.svelte";
  import { api, listAll, runApi } from "../../lib/api";
  import { request, type Input, type ResourceAction } from "../../lib/resourceActions";
  import { toRuleRows, toRules, type RuleRow } from "../../lib/resources/firewallRules";
  import { menu, tabs as tabActions } from "../../lib/resources/firewalls";
  import { showError } from "../../lib/toast";
  import { href } from "../../state";
  import RulesEditor from "./RulesEditor.svelte";
  import type { Firewall } from "./Section.svelte";

  // The detail page of one firewall: Rules (the rules editor, saved through
  // set_rules), Resources (servers and label selectors with apply and remove;
  // the servers a selector matches are shown under it) and Labels; the
  // actions menu deletes it (back to the list afterwards).
  let { project, id, tab, list }: { project: string; id: number; tab: string; list: string } = $props();

  type Named = { id: number; name: string };

  const path = $derived(`/firewalls/${id}`);
  const tabs = $derived(
    [
      { id: "rules", label: "Rules" },
      { id: "resources", label: "Resources" },
      { id: "labels", label: "Labels" },
    ].map((t) => ({ ...t, href: href({ area: "project", project, view: "firewalls", page: { kind: "detail", id, tab: t.id } }) })),
  );
  const current = $derived(tabs.some((t) => t.id === tab) ? tab : "rules");

  let firewall = $state<Firewall | undefined>();
  let servers = $state<Named[]>([]);
  let notFound = $state(false);
  let failing = false;
  // The rows of the rules editor; taken from the firewall when it first
  // loads and after each save, so the refresh does not overwrite an edit.
  let rows = $state<RuleRow[] | undefined>();

  async function load() {
    try {
      const [f, s] = await Promise.all([
        api<{ firewall: Firewall }>(project, "GET", path),
        listAll<Named>(project, "/servers", "servers"),
      ]);
      firewall = f.firewall;
      servers = s;
      if (rows === undefined) rows = toRuleRows(f.firewall.rules);
      notFound = false;
      failing = false;
    } catch (err) {
      if ((err as { code?: unknown }).code === "not_found") notFound = true;
      else if (!failing) showError("Load firewall", err);
      failing = true;
    }
  }

  // The open firewall refreshes every 5 s while it is shown.
  $effect(() => {
    void path;
    untrack(() => {
      firewall = undefined;
      rows = undefined;
      notFound = false;
    });
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });

  const nameOf = (sid: number) => servers.find((s) => s.id === sid)?.name ?? `${sid}`;
  const serverPage = (sid: number) =>
    href({ area: "project", project, view: "servers", page: { kind: "detail", id: sid, tab: "overview" } });

  function done(a: ResourceAction) {
    if (a.id === "delete") location.hash = list;
    else load();
  }

  async function run(a: ResourceAction, input: Input): Promise<boolean> {
    if (!firewall) return false;
    const req = request(a, firewall.id, input)!;
    const res = await runApi(project, `${a.label} ${firewall.name}`, req.method, req.path, req.body);
    if (res !== undefined) load();
    return res !== undefined;
  }

  // Rules tab: Save rules sends all rows through set_rules.
  let saving = $state(false);

  async function saveRules() {
    if (!rows) return;
    saving = true;
    const ok = await run(tabActions.set_rules, { rules: toRules(rows) });
    saving = false;
    // The reload that run started takes the saved rules into the editor.
    if (ok) rows = undefined;
  }

  // Resources tab: the apply dialogs and the remove confirmation.
  let serverOpen = $state(false);
  let serverId = $state("");
  let selectorOpen = $state(false);
  let selector = $state("");
  let confirmOpen = $state(false);
  let confirming = $state<{ action: ResourceAction; input: Input; text: string } | undefined>();

  const appliedServers = $derived(
    (firewall?.applied_to ?? []).filter((a) => a.type === "server").map((a) => a.server!.id),
  );
  const selectors = $derived((firewall?.applied_to ?? []).filter((a) => a.type === "label_selector"));
  const candidates = $derived(servers.filter((s) => !appliedServers.includes(s.id)));

  function openServer() {
    serverId = candidates[0] ? `${candidates[0].id}` : "";
    serverOpen = true;
  }

  function applyServer(e: SubmitEvent) {
    e.preventDefault();
    serverOpen = false;
    // Not awaited: the dialog closes at once, the toast follows the request.
    run(tabActions.apply_to_server, { server: serverId });
  }

  function openSelector() {
    selector = "";
    selectorOpen = true;
  }

  function applySelector(e: SubmitEvent) {
    e.preventDefault();
    selectorOpen = false;
    run(tabActions.apply_to_label_selector, { label_selector: selector.trim() });
  }

  function ask(action: ResourceAction, input: Input, text: string) {
    confirming = { action, input, text };
    confirmOpen = true;
  }
</script>

{#if notFound}
  <DetailPage title="Firewall" help="firewalls" {tabs} {current} back={list} notFound>
    {#snippet children()}{/snippet}
  </DetailPage>
{:else if !firewall}
  <p class="muted" aria-busy="true">Loading the firewall…</p>
{:else}
  {@const f = firewall}
  <DetailPage title={f.name} help="firewalls" {tabs} {current} back={list}>
    {#snippet actions()}
      <ResourceActionsMenu {project} id={f.id} name={f.name} actions={menu} ondone={done} />
    {/snippet}
    {#if current === "resources"}
      <section>
        <div class="head">
          <h2>Servers</h2>
          <Button onclick={openServer}>Apply to server</Button>
        </div>
        {#if appliedServers.length === 0}
          <p class="muted">The firewall is applied to no server directly.</p>
        {:else}
          <table>
            <tbody>
              {#each appliedServers as sid (sid)}
                <tr>
                  <td><a href={serverPage(sid)}>{nameOf(sid)}</a></td>
                  <td class="end">
                    <Button
                      variant="danger"
                      onclick={() => ask(tabActions.remove_from_server, { server: sid }, `the server ${nameOf(sid)}`)}>Remove</Button
                    >
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
        <div class="head">
          <h2>Label selectors</h2>
          <Button onclick={openSelector}>Apply to label selector</Button>
        </div>
        {#if selectors.length === 0}
          <p class="muted">The firewall is applied to no label selector.</p>
        {:else}
          <table>
            <tbody>
              {#each selectors as a (a.label_selector?.selector)}
                {@const expr = a.label_selector?.selector ?? ""}
                <tr>
                  <td>
                    <code>{expr}</code>
                    {#if (a.applied_to_resources ?? []).length === 0}
                      <div class="muted">No server matches.</div>
                    {:else}
                      <ul>
                        {#each a.applied_to_resources ?? [] as r (r.server.id)}
                          <li><a href={serverPage(r.server.id)}>{nameOf(r.server.id)}</a></li>
                        {/each}
                      </ul>
                    {/if}
                  </td>
                  <td class="end">
                    <Button
                      variant="danger"
                      onclick={() => ask(tabActions.remove_from_label_selector, { label_selector: expr }, `the label selector ${expr}`)}
                      >Remove</Button
                    >
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </section>
    {:else if current === "labels"}
      <LabelsTab {project} {path} labels={f.labels} onsaved={load} />
    {:else if rows}
      <RulesEditor bind:rows />
      <div>
        <Button variant="primary" disabled={saving} onclick={saveRules}>Save rules</Button>
      </div>
    {/if}
  </DetailPage>

  <Dialog bind:open={serverOpen} title={`Apply ${f.name} to a server`}>
    <form onsubmit={applyServer}>
      <Field label="Server">
        {#snippet children(c)}
          <select id={c.id} aria-describedby={c.describedby} bind:value={serverId}>
            {#each candidates as s (s.id)}<option value={`${s.id}`}>{s.name}</option>{/each}
          </select>
        {/snippet}
      </Field>
      <div class="buttons">
        <Button onclick={() => (serverOpen = false)}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={serverId === ""}>Apply to server</Button>
      </div>
    </form>
  </Dialog>

  <Dialog bind:open={selectorOpen} title={`Apply ${f.name} to a label selector`}>
    <form onsubmit={applySelector}>
      <Field label="Label selector" hint="Servers whose labels match, for example env=prod.">
        {#snippet children(c)}
          <input id={c.id} aria-describedby={c.describedby} autocomplete="off" spellcheck="false" bind:value={selector} />
        {/snippet}
      </Field>
      <div class="buttons">
        <Button onclick={() => (selectorOpen = false)}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={selector.trim() === ""}>Apply to label selector</Button>
      </div>
    </form>
  </Dialog>

  <ConfirmDialog
    bind:open={confirmOpen}
    title={confirming ? `${confirming.action.label} from ${f.name}` : ""}
    confirmLabel="Remove"
    onconfirm={() => {
      if (confirming) run(confirming.action, confirming.input);
    }}
  >
    <p>Remove <strong>{confirming?.text ?? ""}</strong> from <strong>{f.name}</strong>?</p>
  </ConfirmDialog>
{/if}

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--s3);
    max-width: 800px;
    margin: var(--s4) 0 var(--s3);
  }
  table {
    border-collapse: collapse;
    max-width: 800px;
    width: 100%;
  }
  td {
    text-align: left;
    vertical-align: top;
    padding: var(--s2);
    border-bottom: 1px solid var(--border);
  }
  .end {
    text-align: right;
  }
  ul {
    margin: var(--s1) 0 0;
    padding-left: var(--s4);
  }
  form {
    display: flex;
    flex-direction: column;
  }
  select,
  input {
    width: 100%;
  }
  .buttons {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s4);
  }
  .muted {
    color: var(--muted);
  }
</style>
