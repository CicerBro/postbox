import * as React from "react";
import * as SelectPrimitive from "@radix-ui/react-select";
import { Check, ChevronDown, ChevronUp } from "lucide-react";
import { cn } from "../../lib/utils";

export interface SelectFieldProps {
  value: string;
  onValueChange: (value: string) => void;
  options: { value: string; label: string; disabled?: boolean }[];
  id?: string;
  "aria-label"?: string;
  "aria-describedby"?: string;
  disabled?: boolean;
  placeholder?: string;
  className?: string;
  name?: string;
  required?: boolean;
}
const emptyValue = "__postbox_empty_select_value__";

export const SelectField = React.forwardRef<
  HTMLButtonElement,
  SelectFieldProps
>(
  (
    {
      value,
      onValueChange,
      options,
      id,
      disabled,
      placeholder,
      className,
      name,
      required,
      ...aria
    },
    ref,
  ) => (
    <SelectPrimitive.Root
      value={
        value ||
        (options.some((option) => option.value === "") && !required
          ? emptyValue
          : "")
      }
      onValueChange={(next) => onValueChange(next === emptyValue ? "" : next)}
      disabled={disabled}
      name={name}
      required={required}
    >
      <SelectPrimitive.Trigger
        ref={ref}
        id={id}
        className={cn("select-trigger", className)}
        {...aria}
      >
        <SelectPrimitive.Value
          placeholder={
            placeholder ?? options.find((option) => option.value === "")?.label
          }
        />
        <SelectPrimitive.Icon asChild>
          <ChevronDown size={16} />
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content
          className="select-content"
          position="popper"
          sideOffset={4}
        >
          <SelectPrimitive.ScrollUpButton className="select-scroll">
            <ChevronUp size={14} />
          </SelectPrimitive.ScrollUpButton>
          <SelectPrimitive.Viewport className="select-viewport">
            {options
              .filter((option) => !required || option.value !== "")
              .map((option) => (
                <SelectPrimitive.Item
                  key={option.value}
                  value={option.value || emptyValue}
                  disabled={option.disabled}
                  className="select-item"
                >
                  <SelectPrimitive.ItemText>
                    {option.label}
                  </SelectPrimitive.ItemText>
                  <SelectPrimitive.ItemIndicator className="select-indicator">
                    <Check size={14} />
                  </SelectPrimitive.ItemIndicator>
                </SelectPrimitive.Item>
              ))}
          </SelectPrimitive.Viewport>
          <SelectPrimitive.ScrollDownButton className="select-scroll">
            <ChevronDown size={14} />
          </SelectPrimitive.ScrollDownButton>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  ),
);
SelectField.displayName = "SelectField";
