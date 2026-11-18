import { type ReactNode, useId } from "react";
import { NativeSelect } from "@/components/form-field";

// one select of a filter bar: its caption above it, applied on change
export function SelectFilter({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-caption text-fg-2">
        {label}
      </label>
      <NativeSelect
        id={id}
        size="sm"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full sm:w-auto sm:max-w-64"
      >
        {children}
      </NativeSelect>
    </div>
  );
}
