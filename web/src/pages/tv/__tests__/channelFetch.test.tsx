import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChannelEditor } from "../ChannelEditor";

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

function server(fetchAvailable: boolean) {
  const sent: { method: string; url: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/api/channels/options"))
        return json({ genres: [], decades: [], titles: [], profiles: [], fetchAvailable, fetchGlobalGB: 200 });
      if (url.endsWith("/api/channels/preview")) return json({ movies: 0, series: 0, episodes: 0, items: [] });
      if (url.includes("/api/channels/fetch/search"))
        return json([
          { kind: "series", tmdbId: 100, title: "Друзья", year: 1994 },
          { kind: "movie", tmdbId: 5, title: "Фильм", year: 2001 },
        ]);
      if (url.includes("/api/channels/fetch/seasons")) return json([1, 2]);
      if (init?.method === "POST" || init?.method === "PUT") {
        sent.push({ method: init.method, url, body: JSON.parse(String(init.body)) });
        return json(url.includes("/budget") ? { globalGB: 300 } : saved, init.method === "POST" ? 201 : 200);
      }
      return json({ error: "unexpected " + url }, 500);
    }),
  );
  return sent;
}

afterEach(() => vi.unstubAllGlobals());

describe("ChannelEditor: a channel with fetching", () => {
  it("is offered to an admin where the server fetches, and saves the titles in order", async () => {
    const sent = server(true);
    render(<ChannelEditor channel={null} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />);
    await userEvent.click(await screen.findByRole("radio", { name: "С докачкой" }));
    await userEvent.type(screen.getByLabelText("Фильм или сериал"), "др");
    await userEvent.click(await screen.findByRole("button", { name: "Сезоны: Друзья" }));
    await userEvent.click(await screen.findByRole("button", { name: /Друзья · Сезон 2/ }));
    await userEvent.click(screen.getByRole("button", { name: "Добавить: Фильм" }));
    await userEvent.type(screen.getByLabelText("Название"), "Марафон");
    await userEvent.click(screen.getByRole("button", { name: "Создать" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    const rules = sent[0]!.body.rules as { fetch?: { titles: unknown[] }; order?: string; picks?: unknown };
    expect(rules.order).toBe("ordered");
    expect(rules.picks).toBeUndefined();
    expect(rules.fetch?.titles).toEqual([
      { kind: "series", tmdbId: 100, title: "Друзья", year: 1994, season: 2 },
      { kind: "movie", tmdbId: 5, title: "Фильм", year: 2001 },
    ]);
  });

  it("is not offered where nothing fetches, nor to a profile", async () => {
    server(false);
    const { unmount } = render(<ChannelEditor channel={null} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />);
    await screen.findByRole("radio", { name: "Свой список" });
    expect(screen.queryByRole("radio", { name: "С докачкой" })).toBeNull();
    unmount();
    server(true);
    render(<ChannelEditor channel={null} me={{ id: 2, isAdmin: false }} onClose={() => {}} onSaved={() => {}} />);
    await screen.findByRole("radio", { name: "Свой список" });
    expect(screen.queryByRole("radio", { name: "С докачкой" })).toBeNull();
  });
});
