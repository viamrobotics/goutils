import { expect, test } from "@playwright/test";

test("receives responses", async ({ page }) => {
  await page.goto("/");
  const table: [string, string[]][] = [
    ["unary-wrtc", ["hello"]],
    ["multi-wrtc", ["h", "e", "l", "l", "o", "?"]],
    ["bidi-wrtc", ["o", "n", "e", "t", "w", "o"]],
    ["unary-direct", ["hello"]],
    ["multi-direct", ["h", "e", "l", "l", "o", "?"]],
    ["bidi-direct", []],
  ];

  for (const [testID, expected] of table) {
    const messages = page.getByTestId(testID).getByTestId("message");
    await expect(messages).toHaveCount(expected.length);
    await expect(messages).toContainText(expected);
  }
});
