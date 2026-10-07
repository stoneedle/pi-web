import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, collapseScratchpad } from "../lib/test";
import { buildSession, uniqueSessionName, writeSession } from "../lib/sessions";

test("manual session names commit even unchanged and survive reopening", async ({ page, sessionsDir }, testInfo) => {
  const { entries } = buildSession();
  const id = writeSession(sessionsDir, uniqueSessionName(testInfo, "session-name"), entries);
  const file = join(sessionsDir, "--home-user-demo-project--", id);
  await collapseScratchpad(page);
  await page.goto(`/session?id=${encodeURIComponent(id)}`);
  await expect(page.locator("#session-header-title")).toHaveText("Initial prompt.");

  const rename = async (name: string) => {
    await page.getByRole("button", { name: "Session actions", exact: true }).click();
    page.once("dialog", (dialog) => dialog.accept(name));
    const committed = page.waitForResponse((response) => response.url().includes("/api/rename-session?") && response.request().method() === "POST");
    await page.locator('[data-action="rename"]:visible').click();
    const response = await committed;
    expect(response.status()).toBe(200);
    expect((await response.json()).name).toBe(name);
    const saved = readFileSync(file, "utf8").trim().split("\n").map((line) => JSON.parse(line));
    expect(saved.at(-1)).toMatchObject({ type: "session_info", name });
    await expect(page.locator("#session-header-title")).toHaveText(name);
  };

  await rename("Initial prompt.");
  const manual = "网页手动命名 👨‍👩‍👧‍👦";
  await rename(manual);
  await page.reload();
  await expect(page.locator("#session-header-title")).toHaveText(manual);
  await expect(page).toHaveTitle(manual);
});
