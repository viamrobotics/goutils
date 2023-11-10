import * as PW from "@playwright/test";

PW.test("receives responses", async ({ page }) => {
  page.on("console", async (msg: PW.ConsoleMessage) => {
    await page.evaluate((text: string) => {
      const div = document.createElement("div");
      div.innerText = text;
      div.setAttribute("data-testid", "log");
      document.body.appendChild(div);
    }, msg.text());
  });

  await page.goto("/");
  await page.waitForResponse("**/Echo");
  await page.waitForResponse("**/EchoMultiple");
  await page.waitForResponse("**/EchoBiDi");
  await page.waitForResponse("**/EchoBiDi");
  const texts = await page.getByTestId("log").allInnerTexts();
  const expected = [
    "WebRTC",
    "hello",
    "h",
    "e",
    "l",
    "l",
    "o",
    "?",
    "o",
    "n",
    "e",
    "t",
    "w",
    "o",
    "Direct",
    "hello",
    "h",
    "e",
    "l",
    "l",
    "o",
    "?",
    "o",
    "n",
    "e",
  ];
  PW.expect(texts).toStrictEqual(expected);
});
