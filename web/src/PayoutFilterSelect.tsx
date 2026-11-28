import * as Select from "@radix-ui/react-select";
import { CaretDown, Check } from "@phosphor-icons/react";

type Option = { value: string; label: string };

export function PayoutFilterSelect({ id, label, all, options, value, onChange }: {
  id: string; label: string; all?: string; options: Option[]; value: string; onChange: (value: string) => void;
}) {
  // Prefix real values so the "all" item can never collide with a merchant ID.
  const items = options.map(o => ({ ...o, value: "v:" + o.value }));
  if (all !== undefined) items.unshift({ value: "all", label: all });
  return <div className="payout-filter-field">
    <label htmlFor={id}>{label}</label>
    <Select.Root value={value ? "v:" + value : "all"} onValueChange={key => onChange(key === "all" ? "" : key.slice(2))}>
      <Select.Trigger id={id} className="payout-filter-trigger">
        <Select.Value /><Select.Icon asChild><CaretDown size={14} /></Select.Icon>
      </Select.Trigger>
      <Select.Content position="popper" align="start" sideOffset={5} collisionPadding={10} className="payout-filter-menu">
        <Select.Viewport>{items.map(item => <Select.Item key={item.value} value={item.value} className="payout-filter-option">
          <Select.ItemText>{item.label}</Select.ItemText>
          <Select.ItemIndicator><Check size={15} weight="bold" /></Select.ItemIndicator>
        </Select.Item>)}</Select.Viewport>
      </Select.Content>
    </Select.Root>
  </div>;
}
