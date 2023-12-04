import * as PW from "@playwright/test";

PW.test("receives responses", async ({ page }) => {
  await page.goto("/");
  const table: [string, string[]][] = [
    ["unary-wrtc", ["hello"]],
    ["multi-wrtc", ["h", "e", "l", "l", "o", "?"]],
    ["bidi-wrtc", ["o", "n", "e", "t", "w", "o"]],
    ["unary-direct", ["hello"]],
    ["multi-direct", ["h", "e", "l", "l", "o", "?"]],
    // gRPC-web does not yet support bidirectional streaming so we expect to
    // only receive a response to our first request.
    ["bidi-direct", ["o", "n", "e"]],
  ];

  for (const [testID, expected] of table) {
    const messages = page.getByTestId(testID).locator('div');
    const contents = await messages.allTextContents();

    expect(contents).toStrictEqual(expected);
  }
});
