import { fireEvent } from "@testing-library/react";
import { onTestFinished, vi } from "vitest";

/**
 * What a test needs to place a (i)'s tip, which jsdom cannot lay out: the tip
 * gets a size, the (i) a place, and a touch pins the tip the way a phone does.
 * A tip of 100 x 40 opens above a 20px (i) at (left, top), centred on it: for
 * an (i) at (400, 300) its style is left 360px, top 252px.
 */
const TIP = { width: 100, height: 40, hint: 20 } as const;

/** Gives every element the size of a tip until the test finishes. */
export function stubTipSize(): void {
  const sizes = [
    vi
      .spyOn(HTMLElement.prototype, "offsetWidth", "get")
      .mockReturnValue(TIP.width),
    vi
      .spyOn(HTMLElement.prototype, "offsetHeight", "get")
      .mockReturnValue(TIP.height),
  ];
  onTestFinished(() => sizes.forEach((spy) => spy.mockRestore()));
}

/** Puts the (i) at a place in the viewport. */
export function placeHint(hint: HTMLElement, left: number, top: number): void {
  const size = TIP.hint;
  hint.getBoundingClientRect = () =>
    ({
      left,
      top,
      right: left + size,
      bottom: top + size,
      width: size,
      height: size,
      x: left,
      y: top,
      toJSON: () => ({}),
    }) as DOMRect;
}

/** A touch on the (i), which pins its tip open. */
export function pinTipByTouch(hint: HTMLElement): void {
  // jsdom has no PointerEvent: a plain event carries the pointer type.
  const touch = new Event("pointerdown", { bubbles: true });
  Object.defineProperty(touch, "pointerType", { value: "touch" });
  fireEvent(hint, touch);
  fireEvent.click(hint);
}
