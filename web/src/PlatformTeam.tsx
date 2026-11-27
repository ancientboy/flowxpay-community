import { useState } from "react";
import { Dialog } from "./Dialog";
import { useLocale } from "./locale";
import { platformAPI } from "./platformEnvironment";
import { InventoryForm, LoadState, useWorkspaceData, WorkspaceTable } from "./WorkspaceInventoryUI";

export const platformTeamPath = platformAPI + "/platform-team";

export type PlatformTeamMember = {
  id: string;
  email: string;
  role: "operator" | "viewer";
  active: boolean;
  platform_owner: boolean;
  setup_expires_at: string | null;
};

type Invitation = { email: string; initial_password: string; expires_at: string };

export function PlatformTeam({ token }: { token: string }) {
  const { t, dateTime, localizedLink } = useLocale();
  const state = useWorkspaceData<PlatformTeamMember[]>(platformTeamPath, token);
  const [adding, setAdding] = useState(false);
  const [selected, select] = useState<PlatformTeamMember>();
  const [invitation, showInvitation] = useState<Invitation>();
  return <section className="workspace-panel">
    <div className="heading panel-heading"><h2>{t("Platform member accounts")}</h2>
      <button type="button" className="primary" onClick={() => setAdding(true)}>{t("Add platform member")}</button>
    </div>
    <LoadState state={state} label={t("Platform team")}>{members =>
      <WorkspaceTable label={t("Platform team")} columns={[t("Email"), t("Role"), t("Status"), t("Actions")]}>
        {members.map(member => <tr key={member.id}>
          <td>{member.email}</td>
          <td>{t(member.platform_owner ? "Platform owner" : member.role === "operator" ? "Platform operator" : "Read-only operator")}</td>
          <td>{t(!member.active ? "Disabled" : member.setup_expires_at
            ? new Date(member.setup_expires_at).getTime() > Date.now() ? "Awaiting first-time setup" : "Expired"
            : "Active")}</td>
          <td>{member.active && !member.platform_owner &&
            <button type="button" className="secondary" onClick={() => select(member)}>{t("Disable access")}</button>}</td>
        </tr>)}
      </WorkspaceTable>}</LoadState>
    {adding && <InventoryForm<Invitation> token={token} path={platformTeamPath} title={t("Add platform member")}
      close={() => setAdding(false)} created={result => { setAdding(false); showInvitation(result); state.refresh(); }}
      payload={fields => ({ action: "invite", email: fields.get("email"), role: fields.get("role") })}>
      <label htmlFor="new-platform-email">{t("Email")}</label>
      <input id="new-platform-email" name="email" type="email" maxLength={254} required />
      <label htmlFor="new-platform-role">{t("Role")}</label>
      <select id="new-platform-role" name="role" defaultValue="operator">
        <option value="operator">{t("Platform operator")}</option>
        <option value="viewer">{t("Read-only operator")}</option>
      </select>
    </InventoryForm>}
    {selected && <InventoryForm token={token} path={platformTeamPath} title={t("Disable platform member")}
      close={() => select(undefined)} created={() => { select(undefined); state.reload(); }}
      submitLabel="Disable access" payload={() => ({ action: "disable", id: selected.id })}>
      <p className="wrap">{selected.email}</p>
      <p>{t("Disabling this account ends its active sessions immediately.")}</p>
    </InventoryForm>}
    {invitation && <Dialog drawer title={t("Platform member created")} close={() => showInvitation(undefined)}>
      <p className="wrap">{invitation.email}</p>
      <p>{t("Share the initial password privately with the administrator. It expires in 24 hours and can only be viewed here.")}</p>
      <label htmlFor="platform-initial-password">{t("Initial password")}</label>
      <input id="platform-initial-password" value={invitation.initial_password} readOnly autoComplete="off"
        onFocus={event => event.currentTarget.select()} />
      <p>{t("Expires")} · {dateTime(invitation.expires_at)}</p>
      <div className="detail-actions">
        <a href={localizedLink("/flowxpay/ops/")} target="_blank" rel="noreferrer">{t("Operator sign-in")}</a>
        <button type="button" className="primary" onClick={() => showInvitation(undefined)}>{t("Done")}</button>
      </div>
    </Dialog>}
  </section>;
}
