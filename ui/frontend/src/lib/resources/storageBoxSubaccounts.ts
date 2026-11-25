// Storage box subaccount actions of the Subaccounts tab. The resource ID is
// the box's; input.subaccount is the subaccount's ID.
import { pick, type Input, type ResourceAction } from "../resourceActions";

const at = (box: number, i: Input) => `/storage_boxes/${box}/subaccounts/${i.subaccount}`;

/** subAction is a POST to .../subaccounts/{sub}/actions/<act> with input. */
function subAction(act: string, label: string, body: ResourceAction["body"]): ResourceAction {
  return {
    id: act,
    label,
    method: "POST",
    path: (box, i) => `${at(box, i)}/actions/${act}`,
    body,
    confirm: "none",
    needsInput: true,
  };
}

/** tabs are the actions of the Subaccounts tab and its row menu. */
export const tabs: Record<string, ResourceAction> = {
  create: {
    id: "create",
    label: "Create subaccount",
    method: "POST",
    path: (box) => `/storage_boxes/${box}/subaccounts`,
    body: (i) => pick(i, ["home_directory", "password", "name", "description", "access_settings"]),
    confirm: "none",
    needsInput: true,
  },
  update: {
    id: "update",
    label: "Edit",
    method: "PUT",
    path: at,
    body: (i) => pick(i, ["name", "description"]),
    confirm: "none",
    needsInput: true,
  },
  change_home_directory: subAction("change_home_directory", "Change home directory", (i) => ({
    home_directory: i.home_directory,
  })),
  reset_password: {
    ...subAction("reset_subaccount_password", "Reset password", (i) => ({ password: i.password })),
    id: "reset_password",
  },
  update_access_settings: subAction("update_access_settings", "Update access settings", (i) =>
    pick(i, ["reachable_externally", "readonly", "samba_enabled", "ssh_enabled", "webdav_enabled"]),
  ),
  delete: { id: "delete", label: "Delete", method: "DELETE", path: at, confirm: "typed-name", needsInput: false },
};
