/**
 * Contract: a settings frame never widens the settings panel.
 *
 * A <fieldset> defaults to a min-content inline size. Nested frames - a
 * per-chat override inside a bot's block on the Gateways tab - added their
 * padding up past a 390px panel and scrolled it sideways. jsdom does no
 * layout, so the rule is pinned here.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

test("every settings fieldset may shrink below its content", () => {
  const body = [
    ...css.matchAll(/(?:^|\})\s*\.settings-fieldset\s*\{([^}]*)\}/g),
  ]
    .map((m) => m[1])
    .join(";");
  expect(body).toMatch(
    /(?:^|[;\s])(?:min-inline-size|min-width)\s*:\s*0(?:px)?\s*(?:;|$)/,
  );
});

/** Collapses whitespace, which Prettier spreads values over lines with. */
const squash = (text: string) => text.replace(/\s+/g, " ").trim();

/** Every rule with its selector list and body (a rule inside an @media too). */
function rules(source: string): { selectors: string[]; body: string }[] {
  return [...source.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({
    selectors: m[1]!.split(",").map((s) => squash(s)),
    body: squash(m[2]!),
  }));
}

/** The text of every `@media (prefers-reduced-motion: reduce)` block. */
function reducedMotionBlocks(source: string): string[] {
  const opener = "@media (prefers-reduced-motion: reduce) {";
  const blocks: string[] = [];
  for (let at = source.indexOf(opener); at !== -1; ) {
    let depth = 0;
    let i = at + opener.length - 1;
    for (; i < source.length; i++) {
      if (source[i] === "{") depth++;
      else if (source[i] === "}" && --depth === 0) break;
    }
    blocks.push(source.slice(at + opener.length, i));
    at = source.indexOf(opener, i);
  }
  return blocks;
}

test("the setting the search opened is ringed as a state that reduced motion keeps", () => {
  // The ring is the state itself, a ring and a wash in the accent, and the
  // fade is only the animation on top of it.
  const ring = rules(css).find((r) =>
    r.selectors.includes(".is-search-target"),
  );
  expect(ring?.body).toContain(
    "box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 55%, transparent), inset 0 0 0 100vmax color-mix(in srgb, var(--accent) 10%, transparent)",
  );
  expect(ring?.body).toContain("animation: settings-search-target 1.6s");
  const fade = css.slice(css.indexOf("@keyframes settings-search-target"));
  expect(squash(fade.slice(0, fade.indexOf("}\n}") + 3))).toMatch(
    /^@keyframes settings-search-target \{ 62\.5% \{ box-shadow:.*\} 100% \{ box-shadow: 0 0 0 2px transparent, inset 0 0 0 100vmax transparent; \} \}$/,
  );

  // Under reduced motion the animation goes and the ring holds still until the
  // script takes the class off. The rule joins the settings block: the swarm
  // screen's test reads the last reduced-motion block of the sheet.
  const blocks = reducedMotionBlocks(css);
  const settings = blocks.find((b) => b.includes(".settings-form-anim"));
  expect(settings).toBeDefined();
  const off = rules(settings!).find((r) =>
    r.selectors.includes(".is-search-target"),
  );
  expect(off?.body).toBe("animation: none;");
  expect(blocks.at(-1)).not.toContain(".is-search-target");
});

test("the settings search field and its results share the documentation search's rules", () => {
  const all = rules(css);
  const pairs: [string, string][] = [
    [".docs-search", ".settings-search-input"],
    [".docs-search:focus-visible", ".settings-search-input:focus-visible"],
    [".docs-search-kbd", ".settings-search-kbd"],
    [".docs-hits", ".settings-hits"],
    [".docs-hit", ".settings-hit"],
    [".docs-hit:hover", ".settings-hit:hover"],
    [".docs-hit:focus-visible", ".settings-hit:focus-visible"],
    [".docs-hit.is-selected", ".settings-hit.is-selected"],
    [".docs-hit-title", ".settings-hit-title"],
    [".docs-hit-snippet", ".settings-hit-where"],
    [".docs-hit-snippet", ".settings-hit-desc"],
    [".docs-hit-snippet mark", ".settings-hit mark"],
    [".docs-empty", ".settings-search-empty"],
  ];
  for (const [docs, settings] of pairs) {
    const rule = all.find((r) => r.selectors.includes(docs));
    expect(rule, `a rule for ${docs}`).toBeDefined();
    expect(rule!.selectors, `${settings} shares the rule of ${docs}`).toContain(
      settings,
    );
  }
});
