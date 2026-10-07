import { rmSync } from "node:fs";
import { test, expect, collapseScratchpad, isMobileLayout } from "../lib/test";
import {
  buildSession,
  realWorkingDir,
  uniqueSessionName,
  writeSession,
} from "../lib/sessions";

for (const multiSelect of [false, true]) {
  test(`${multiSelect ? "multiple-choice" : "single-choice"} answers stay in their session after SPA navigation`, async ({
    page,
    sessionsDir,
  }, testInfo) => {
    const cwd = realWorkingDir();
    try {
      const sessions = [0, 1, 2].map((index) => {
        const name = uniqueSessionName(testInfo, `question-${index}`);
        const question = `Choose for ${name}`;
        const { entries, lastId } = buildSession({ cwd });
        const id = writeSession(sessionsDir, name, [
          ...entries,
          {
            type: "message",
            id: `${name}-question`,
            parentId: lastId,
            timestamp: new Date().toISOString(),
            message: {
              role: "assistant",
              content: [
                {
                  type: "toolCall",
                  id: `${name}-call`,
                  name: "pi_web_ask_user_question",
                  arguments: {
                    questions: [
                      {
                        question,
                        multiSelect,
                        options: [{ label: "A" }, { label: "B" }],
                      },
                    ],
                  },
                },
              ],
              timestamp: Date.now(),
            },
          },
        ]);
        return { id, question };
      });
      const initialEntries = await Promise.all(
        sessions.map(async ({ id }) => {
          const response = await page.request.get(
            `/api/session?id=${encodeURIComponent(id)}`,
          );
          expect(response.ok()).toBe(true);
          return (await response.json()).entries;
        }),
      );
      const postedSessions: string[] = [];
      page.on("request", (request) => {
        const url = new URL(request.url());
        if (request.method() === "POST" && url.pathname === "/api/chat") {
          postedSessions.push(url.searchParams.get("id") || "");
        }
      });

      await collapseScratchpad(page);
      await page.goto(`/session?id=${encodeURIComponent(sessions[0].id)}`);
      await page.evaluate(() => {
        document.documentElement.dataset.questionNavigation = "same-document";
      });
      for (const { id } of sessions.slice(1)) {
        if (await isMobileLayout(page)) {
          await page.locator("#tree-toggle").dispatchEvent("click");
          await expect(page.locator("#sidebar")).toHaveClass(/open/);
        }
        await page
          .locator('[role="tab"][aria-controls="sidebar-sessions-panel"]')
          .click();
        await page
          .locator(
            `.sidebar-session-row[href="/session?id=${encodeURIComponent(id)}"]`,
          )
          .click();
        await expect(page.locator("#pi-chat-composer")).toHaveAttribute(
          "data-session-id",
          id,
        );
      }
      expect(
        await page.evaluate(
          () => document.documentElement.dataset.questionNavigation,
        ),
      ).toBe("same-document");

      const current = sessions[2];
      const card = page.locator(".ask-question-card", {
        hasText: current.question,
      });
      await card.locator('[data-answer="A"]').click();
      if (multiSelect) {
        await card.locator('[data-answer="B"]').click();
        expect(postedSessions).toEqual([]);
        await card.locator(".ask-question-submit-btn").click();
      }
      const answer = `"${current.question}" = "${multiSelect ? "A, B" : "A"}"`;
      await expect(page.locator("#messages")).toContainText(
        `Stub reply: ${answer}`,
        {
          timeout: 20000,
        },
      );
      expect(postedSessions).toEqual([current.id]);

      for (let index = 0; index < sessions.length; index += 1) {
        const response = await page.request.get(
          `/api/session?id=${encodeURIComponent(sessions[index].id)}`,
        );
        expect(response.ok()).toBe(true);
        const { entries } = await response.json();
        if (index < 2) {
          expect(entries).toEqual(initialEntries[index]);
        } else {
          const answers = entries.filter(
            (entry: {
              message?: { role?: string; content?: { text?: string }[] };
            }) =>
              entry.message?.role === "user" &&
              entry.message.content?.some((part) => part.text === answer),
          );
          expect(answers).toHaveLength(1);
        }
      }
    } finally {
      rmSync(cwd, { recursive: true, force: true });
    }
  });
}
