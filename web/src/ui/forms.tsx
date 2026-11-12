import { cloneElement, isValidElement, useId } from "react";
import type { ReactElement, ReactNode, SelectHTMLAttributes } from "react";

import { Icon } from "./Icon";

type FieldProps = {
  label: ReactNode;
  // source names where a value comes from: Built-in, Default, Installation, Project, and the like.
  source?: { name: string; overridden?: boolean };
  help?: ReactNode;
  // A warning never blocks saving; an error does, and the control says it is invalid.
  warning?: ReactNode;
  error?: ReactNode;
  // children is the one control the label names. Field gives it its ID and its descriptions.
  children: ReactElement<{ id?: string; "aria-describedby"?: string; "aria-invalid"?: boolean }>;
};

// Field is a label above a control, with its source, its help below, and a warning or an error that
// names the fix. The control learns its label, help, and message by ID.
export function Field({ label, source, help, warning, error, children }: FieldProps) {
  const id = useId();
  const helpId = `${id}-help`;
  const messageId = `${id}-message`;
  const described = [help ? helpId : "", error || warning ? messageId : ""].filter(Boolean).join(" ") || undefined;
  const control = isValidElement(children) ? cloneElement(children, { id, "aria-describedby": described, "aria-invalid": error ? true : undefined }) : children;
  return (
    <div className={`field${error ? " has-error" : warning ? " has-warning" : ""}`}>
      <label className="label" htmlFor={id}>
        {label}
        {source ? <span className={`source${source.overridden ? " override" : ""}`}>{source.name}</span> : null}
      </label>
      {control}
      {help ? (
        <p className="help" id={helpId}>
          {help}
        </p>
      ) : null}
      {error ? (
        <p className="msg msg-error" id={messageId}>
          <Icon name="warning" size="sm" />
          <span>{error}</span>
        </p>
      ) : warning ? (
        <p className="msg msg-warning" id={messageId}>
          <Icon name="warning" size="sm" />
          <span>{warning}</span>
        </p>
      ) : null}
    </div>
  );
}

// Select is a native select with the chevron drawn beside it, since the page loads no image from a data URI.
export function Select({ small = false, className = "", children, ...rest }: SelectHTMLAttributes<HTMLSelectElement> & { small?: boolean }) {
  return (
    <span className="select-wrap">
      <select className={`select${small ? " select-sm" : ""}${className ? ` ${className}` : ""}`} {...rest}>
        {children}
      </select>
      <Icon name="chevron-down" />
    </span>
  );
}
