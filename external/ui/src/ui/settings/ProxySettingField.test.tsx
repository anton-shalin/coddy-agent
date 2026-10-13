import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ProxySettingField } from "./ProxySettingField";
import type { OverrideTexts } from "./settingsTexts";

afterEach(cleanup);

const TEXTS: OverrideTexts = {
  primary: "Proxy URL",
  primaryDesc: "Where the requests go",
  second: "Ignore system proxy",
  secondDesc: "Connect directly",
};

function tipOf(hint: HTMLElement): string | null {
  fireEvent.focus(hint);
  const tip = screen.queryByRole("tooltip")?.textContent ?? null;
  fireEvent.blur(hint);
  return tip;
}

test("the switch is drawn from the second text and the URL field from the primary one, each with its own description", () => {
  render(<ProxySettingField value="" onChange={() => {}} texts={TEXTS} />);

  expect(
    screen.getByRole("switch", { name: "Ignore system proxy" }),
  ).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Proxy URL" })).toBeTruthy();
  const hints = screen.getAllByTestId("field-hint");
  expect(hints).toHaveLength(2);
  expect(tipOf(hints[0]!)).toBe("Connect directly");
  expect(tipOf(hints[1]!)).toBe("Where the requests go");
});

test("a text without a description draws no (i) for it", () => {
  render(
    <ProxySettingField
      value=""
      onChange={() => {}}
      texts={{ primary: "Proxy URL", second: "Ignore system proxy" }}
    />,
  );

  expect(screen.queryAllByTestId("field-hint")).toHaveLength(0);
});
