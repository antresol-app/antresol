import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChannelEditor } from "../ChannelEditor";
import type { Channel } from "../../../api/channels";

const json = (b: unknown, status = 200) =>
  new Response(JSON.stringify(b), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const saved = {
  id: 7,
  number: 7,
  name: "x",
  category: "",
  personal: false,
  auto: false,
  enabled: true,
  canEdit: true,
  logo: 0,
};

function server() {
  const sent: {
    method: string;
    body: { rules: { skipSegments?: boolean } };
  }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/api/channels/options"))
        return json({
          genres: [],
          decades: [],
          titles: [],
          profiles: [],
          extrasAvailable: false,
        });
      if (init?.method === "POST" || init?.method === "PUT") {
        sent.push({ method: init.method, body: JSON.parse(String(init.body)) });
        return json(saved, init.method === "POST" ? 201 : 200);
      }
      return json({ error: "unexpected " + url }, 500);
    }),
  );
  return sent;
}

afterEach(() => vi.unstubAllGlobals());

const label = "Пропускать заставки и титры";

describe("ChannelEditor: skipping openings and credits", () => {
  it("is on for a new channel and sends rules.skipSegments", async () => {
    const sent = server();
    render(
      <ChannelEditor
        channel={null}
        me={{ id: 1, isAdmin: true }}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    const sw = await screen.findByRole("switch", { name: label });
    expect(sw).toBeChecked();
    await userEvent.type(screen.getByLabelText("Название"), "Ситкомы");
    await userEvent.click(screen.getByRole("button", { name: "Создать" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.body.rules.skipSegments).toBe(true);
  });

  it("shows a channel’s rule and turns it off", async () => {
    const sent = server();
    const ch = {
      ...saved,
      name: "Сериал",
      rules: { order: "ordered", skipSegments: true },
    } as unknown as Channel;
    render(
      <ChannelEditor
        channel={ch}
        me={{ id: 1, isAdmin: true }}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    const sw = await screen.findByRole("switch", { name: label });
    expect(sw).toBeChecked();
    await userEvent.click(sw);
    expect(sw).not.toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.method).toBe("PUT");
    expect(sent[0]?.body.rules.skipSegments).toBeUndefined();
  });

  it("an older channel without the rule shows it off", async () => {
    server();
    const ch = { ...saved, name: "Старый", rules: {} } as unknown as Channel;
    render(
      <ChannelEditor
        channel={ch}
        me={{ id: 1, isAdmin: true }}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(
      await screen.findByRole("switch", { name: label }),
    ).not.toBeChecked();
  });
});
