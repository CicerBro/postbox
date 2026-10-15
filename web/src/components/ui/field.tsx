import type { ComponentProps, ReactNode } from "react";
import { Input } from "./input";

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="form-field grid min-w-0 w-full gap-2 text-sm font-medium">
      <span>{label}</span>
      {children}
    </label>
  );
}

export function InputField({ label, ...props }: ComponentProps<typeof Input> & { label: string }) {
  return <Field label={label}><Input {...props} /></Field>;
}
