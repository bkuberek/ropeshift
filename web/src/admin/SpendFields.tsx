import { useId } from "react";

import { TableRegion } from "../ui/display";
import { Field } from "../ui/forms";
import type { PolicyValues } from "./logic";
import { spendNoticeKinds, waitRules, waitRuleWords } from "./spend";

// The controls only the spend policies have: the wait rules a member may turn off, the command prefixes a rule also matches,
// and the channels that carry each kind of spend alert.

// RulesField turns off wait rules, by the ID the policy names them.
export function RulesField({ label, help, value, disabled, onChange }: { label: string; help?: string; value: string[]; disabled: boolean; onChange: (value: string[]) => void }) {
  return (
    <fieldset className="field">
      <legend className="label">{label}</legend>
      <div className="check-list">
        {waitRules.map((rule) => (
          <label key={rule} className="check-row">
            <input
              type="checkbox"
              disabled={disabled}
              checked={value.includes(rule)}
              onChange={(event) => onChange(waitRules.filter((one) => (one === rule ? event.target.checked : value.includes(one))))}
            />
            <span>
              Turn off {rule} <span className="small subtle">{waitRuleWords[rule]}</span>
            </span>
          </label>
        ))}
      </div>
      {help ? <p className="help">{help}</p> : null}
    </fieldset>
  );
}

// PatternsField lists the command prefixes a wait rule also matches, one to a line.
export function PatternsField({ label, help, value, disabled, error, onChange }: { label: string; help?: string; value: string[]; disabled: boolean; error?: string; onChange: (value: string[]) => void }) {
  return (
    <Field label={label} help={help} error={error}>
      <textarea
        className="input mono"
        rows={Math.max(2, value.length + 1)}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled}
        value={value.join("\n")}
        onChange={(event) => onChange(event.target.value === "" ? [] : event.target.value.split("\n"))}
      />
    </Field>
  );
}

type Channels = Record<string, string[]>;

// SpendChannelsField chooses, for each kind of spend alert, the channels beyond the web UI that carry it. The web UI shows
// every alert whatever is chosen. A channel is offered only while the policy turns it on, as the API refuses one that is
// not.
export function SpendChannelsField({ label, help, value, form, disabled, error, onChange }: {
  label: string;
  help?: string;
  value: Channels;
  form: PolicyValues;
  disabled: boolean;
  error?: string;
  onChange: (value: Channels) => void;
}) {
  const note = useId();
  const on: Record<string, boolean> = { desktop: Boolean(form.desktop), webhook: typeof form.webhook_url === "string" && form.webhook_url !== "" };
  const toggle = (kind: string, channel: string, chosen: boolean) => {
    const next = { ...value };
    const channels = (value[kind] ?? []).filter((one) => one !== channel);
    if (chosen) {
      channels.push(channel);
    }
    if (channels.length === 0) {
      delete next[kind];
    } else {
      next[kind] = channels.sort();
    }
    // The kinds go in the ledger's order, so the same choice is always the same body.
    onChange(Object.fromEntries(spendNoticeKinds.filter((one) => next[one.kind]).map((one) => [one.kind, next[one.kind] ?? []])));
  };
  return (
    <fieldset className={`field${error ? " has-error" : ""}`} aria-describedby={error ? note : undefined}>
      <legend className="label">{label}</legend>
      <TableRegion label="Channels for each spend alert">
        <table className="table">
          <caption className="visually-hidden">Channels for each spend alert</caption>
          <thead>
            <tr>
              <th scope="col">Alert</th>
              <th scope="col">Web UI</th>
              <th scope="col">Desktop</th>
              <th scope="col">Webhook</th>
            </tr>
          </thead>
          <tbody>
            {spendNoticeKinds.map(({ kind, words }) => (
              <tr key={kind}>
                <th scope="row">
                  {words} <span className="mono small subtle">{kind}</span>
                </th>
                <td>Always</td>
                {(["desktop", "webhook"] as const).map((channel) => (
                  <td key={channel}>
                    <label className="check-row">
                      <input
                        type="checkbox"
                        disabled={disabled || (!on[channel] && !(value[kind] ?? []).includes(channel))}
                        checked={(value[kind] ?? []).includes(channel)}
                        onChange={(event) => toggle(kind, channel, event.target.checked)}
                      />
                      <span className="visually-hidden">
                        {words}: send to the {channel}
                      </span>
                    </label>
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
      {help ? <p className="help">{help}</p> : null}
      {error ? (
        <p className="msg msg-error" id={note}>
          {error}
        </p>
      ) : null}
    </fieldset>
  );
}
