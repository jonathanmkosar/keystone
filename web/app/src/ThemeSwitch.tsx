import { Icon, type IconName } from "./Icon";
import type { ThemeChoice } from "./theme";

const choices: { value: ThemeChoice; label: string; icon: IconName }[] = [
  { value: "dark", label: "Dark", icon: "moon" },
  { value: "light", label: "Light", icon: "sun" },
  { value: "system", label: "System", icon: "system" },
];

/** Three choices: dark, light, or follow the system. */
export function ThemeSwitch({ choice, onChoose }: { choice: ThemeChoice; onChoose: (c: ThemeChoice) => void }) {
  return (
    <div className="theme-switch" role="radiogroup" aria-label="Theme">
      {choices.map((c) => (
        <button
          key={c.value}
          type="button"
          role="radio"
          aria-checked={choice === c.value}
          title={c.label}
          className={choice === c.value ? "theme-option is-on" : "theme-option"}
          onClick={() => onChoose(c.value)}
        >
          <Icon name={c.icon} size={16} />
          <span className="theme-label">{c.label}</span>
        </button>
      ))}
    </div>
  );
}
