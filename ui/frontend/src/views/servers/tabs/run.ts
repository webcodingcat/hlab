// Runs one entry of the server tab actions under an action toast; shared by
// the detail tabs, which only collect the input and the confirmation.
import { runApi } from "../../../lib/api";
import { request, tabActions, type Input } from "../../../lib/serverActions";

/** runTab sends the tab action id for the server; true when its actions ended well. */
export async function runTab(project: string, server: { id: number; name: string }, id: string, input: Input = {}) {
  const a = tabActions[id];
  const req = request(a, server.id, input);
  if (!req) return false;
  const res = await runApi(project, `${a.label} ${server.name}`, req.method, req.path, req.body);
  return res !== undefined;
}

/** splitIPs reads a comma or space separated list of IPs. */
export const splitIPs = (text: string) => text.split(/[\s,]+/).filter(Boolean);
