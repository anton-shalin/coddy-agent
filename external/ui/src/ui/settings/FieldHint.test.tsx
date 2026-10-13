import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { FieldHint, FieldLabel, LegendWithHint } from "./FieldHint";
import { PaneHidden } from "../components/PanePortal";
import { OpenRailScreen } from "../nav/railEscape.fakes";
import { pinTipByTouch, placeHint, stubTipSize } from "./pinnedTip.fakes";

afterEach(cleanup);

function hint(): HTMLButtonElement {
  return screen.getByTestId("field-hint") as HTMLButtonElement;
}

test("the description is not on the page until the (i) is hovered", () => {
  render(
    <FieldLabel label="Max tokens" description="Upper bound on tokens." />,
  );

  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(document.body.textContent).not.toContain("Upper bound on tokens.");
  expect(hint().getAttribute("aria-label")).toBe("About Max tokens");

  fireEvent.mouseEnter(hint());
  const tip = screen.getByRole("tooltip");
  expect(tip.textContent).toBe("Upper bound on tokens.");
  expect(hint().getAttribute("aria-describedby")).toBe(tip.id);

  fireEvent.mouseLeave(hint());
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(hint().getAttribute("aria-describedby")).toBeNull();
});

// The tip is portalled to <body>: inside the field it would sit in the
// settings scroll container, be clipped by it or grow a scrollbar for it.
test("the tip is rendered in the body, outside the field", () => {
  const { container } = render(
    <div className="settings-scroll">
      <FieldLabel label="Temperature" description="Sampling temperature." />
    </div>,
  );

  fireEvent.mouseEnter(hint());
  const tip = screen.getByRole("tooltip");
  expect(container.contains(tip)).toBe(false);
  expect(tip.parentElement).toBe(document.body);
});

test("keyboard focus opens the tip and blur closes it", () => {
  render(<FieldLabel label="Proxy URL" description="Optional proxy." />);

  fireEvent.focus(hint());
  expect(screen.getByRole("tooltip").textContent).toBe("Optional proxy.");
  fireEvent.blur(hint());
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test("Escape, a scroll and a tap elsewhere close the tip", () => {
  render(<FieldLabel label="Timeout" description="Per request." />);

  fireEvent.click(hint());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByRole("tooltip")).toBeNull();

  fireEvent.click(hint());
  act(() => {
    document.dispatchEvent(new Event("scroll"));
  });
  expect(screen.queryByRole("tooltip")).toBeNull();

  fireEvent.click(hint());
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

// The drawer has listened for Escape since it opened, long before the tip:
// the tip hears the key first all the same, and the drawer stays.
test("Escape takes the tip down and leaves the drawer under it for the next one", () => {
  const closeDrawer = vi.fn();
  render(
    <>
      <OpenRailScreen id="settings" onClose={closeDrawer} />
      <FieldLabel label="Timeout" description="Per request." />
    </>,
  );
  fireEvent.mouseEnter(hint());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(closeDrawer).not.toHaveBeenCalled();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(closeDrawer).toHaveBeenCalledTimes(1);
});

// Inside a <label for> or a <legend> the click must stay with the (i).
test("a click on the (i) does not reach the control around it", () => {
  let clicks = 0;
  render(
    <label
      onClick={() => {
        clicks++;
      }}
    >
      Name
      <FieldHint text="Help" label="Name" />
    </label>,
  );

  fireEvent.click(hint());
  expect(clicks).toBe(0);
  expect(screen.getByRole("tooltip").textContent).toBe("Help");
});

test("a field without a description has a bare label and no (i)", () => {
  const { container } = render(<FieldLabel label="Model id" />);

  expect(container.querySelector(".field-hint")).toBeNull();
  expect(container.querySelector(".settings-label")?.textContent).toBe(
    "Model id",
  );
});

test("a fieldset legend carries its description the same way", () => {
  render(
    <fieldset>
      <LegendWithHint label="Models" description="What the provider lists." />
    </fieldset>,
  );

  expect(screen.getByRole("group", { name: /Models/ })).toBeTruthy();
  fireEvent.mouseEnter(hint());
  expect(screen.getByRole("tooltip").textContent).toBe(
    "What the provider lists.",
  );
});

// A pane hidden under search results is deaf and draws no portal. The tip a
// touch pinned stays open meanwhile: scrolling the results cannot close it,
// and it is measured against its (i) again when the pane is shown.
test("a tip pinned by a touch in a hidden pane is not drawn, survives a scroll and is placed again when shown", () => {
  stubTipSize();
  const pane = (hidden: boolean) => (
    <PaneHidden.Provider value={hidden}>
      <FieldLabel label="Timeout" description="Per request." />
    </PaneHidden.Provider>
  );
  const tip = () => screen.queryByTestId("field-hint-tip");

  const { rerender } = render(pane(false));
  placeHint(hint(), 400, 300);
  pinTipByTouch(hint());
  expect(tip()?.style.left).toBe("360px");
  expect(tip()?.style.top).toBe("252px");

  rerender(pane(true));
  expect(tip()).toBeNull();
  expect(document.body.textContent).not.toContain("Per request.");
  // Nothing the results do closes it: not their scroll, a pointer on them, a
  // resize, nor an Escape that belongs to the screen.
  act(() => {
    document.dispatchEvent(new Event("scroll"));
    window.dispatchEvent(new Event("resize"));
  });
  fireEvent.mouseDown(document.body);
  fireEvent.touchStart(document.body);
  expect(fireEvent.keyDown(document.body, { key: "Escape" })).toBe(true);

  // The (i) moved while the pane was hidden.
  placeHint(hint(), 500, 100);
  rerender(pane(false));
  expect(tip()?.textContent).toBe("Per request.");
  expect(tip()?.style.left).toBe("460px");
  expect(tip()?.style.top).toBe("52px");
  expect(tip()?.style.visibility).toBe("");
  expect(hint().className).toContain("is-open");
  // Still pinned: leaving and blurring the (i) do not take it down ...
  fireEvent.mouseLeave(hint());
  fireEvent.blur(hint());
  expect(tip()).not.toBeNull();
  // ... and it listens again.
  act(() => {
    document.dispatchEvent(new Event("scroll"));
  });
  expect(tip()).toBeNull();
});

// Placement and layering are CSS jsdom cannot lay out: pin the rules that keep
// the tip over every panel and fixed to the viewport.
test("the tip is fixed, layered over every panel and never takes the pointer", () => {
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
    "utf8",
  );
  const rule = css.match(/^\.field-hint-tip\s*\{([^}]*)\}/m);
  expect(rule).not.toBeNull();
  expect(rule![1]).toMatch(/position:\s*fixed/);
  expect(rule![1]).toMatch(/z-index:\s*1000/);
  expect(rule![1]).toMatch(/pointer-events:\s*none/);
  // Higher than anything else the stylesheet stacks.
  const others = [...css.matchAll(/z-index:\s*(\d+)/g)]
    .map((m) => Number(m[1]))
    .filter((n) => n !== 1000);
  expect(Math.max(...others)).toBeLessThan(1000);
});
