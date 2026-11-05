// IMDb and Rotten Tomatoes ratings on a title page: chips on the metadata
// line and rows of «Информация», fetched by the item's IMDb id; nothing
// known (or no id) shows nothing.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import type { ItemDetail as Detail, Ratings } from "../../api/types";
import { setLanguagePref } from "../../i18n";
import { session } from "../../state/session";
import { ItemDetail } from "../ItemDetail";

const json = (b: unknown, status = 200) =>
  new Response(JSON.stringify(b), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const movie = {
  id: 5,
  kind: "movie",
  title: "Интерстеллар",
  subtitle: "",
  year: 2014,
  poster: "",
  backdrop: "",
  logo: "",
  progress: 0,
  onServer: true,
  overview: "Космос",
  genres: ["Фантастика"],
  cast: [],
  files: [],
  inWatchlist: false,
  seasons: [],
  actions: { play: false, toDevice: false, toServer: false, watchNow: false },
  tmdbId: 157336,
  imdbId: "tt0816692",
} as unknown as Detail;

function stub(item: Detail, ratings: Ratings) {
  const f = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/api/items/5")) return json(item);
    if (url.includes("/api/ratings/")) return json(ratings);
    return json({});
  });
  vi.stubGlobal("fetch", f);
  return f;
}
const ratingCalls = (f: ReturnType<typeof stub>) =>
  f.mock.calls
    .map(([u]) => String(u))
    .filter((u) => u.includes("/api/ratings/"));

function renderItem() {
  return render(
    <MemoryRouter initialEntries={["/item/5"]}>
      <Routes>
        <Route path="/item/:id" element={<ItemDetail />} />
      </Routes>
    </MemoryRouter>,
  );
}

function infoRow(label: string, section = "Информация") {
  const info = screen.getByRole("region", { name: section });
  const dt = within(info).queryByText(label, { selector: "dt" });
  return dt?.nextElementSibling?.textContent;
}

describe("ratings on a title page", () => {
  beforeEach(() => session.reset());
  afterEach(() => vi.unstubAllGlobals());

  it("shows IMDb and Rotten Tomatoes chips and info rows", async () => {
    const f = stub(movie, {
      imdb: { rating: 8.7, votes: 2312345 },
      rottenTomatoes: { tomatometer: 73, audience: 86 },
    });
    renderItem();
    const imdb = await screen.findByLabelText("IMDb 8,7 из 10");
    expect(imdb).toHaveTextContent("IMDb8,7");
    expect(imdb.getAttribute("title")).toMatch(/2\s312\s345 оценок/);
    expect(imdb.getAttribute("title")).toContain("Данные IMDb");
    expect(
      screen.getByLabelText("Rotten Tomatoes: критики 73 %"),
    ).toHaveTextContent("RT73 %");
    expect(infoRow("IMDb")).toMatch(/^8,7 · 2\s312\s345 оценок$/);
    expect(infoRow("Rotten Tomatoes")).toBe("критики 73 % · зрители 86 %");
    expect(ratingCalls(f)).toEqual(["/api/ratings/tt0816692"]);
  });

  it("shows only what is known", async () => {
    stub(movie, { imdb: { rating: 7, votes: 1 } });
    renderItem();
    expect(await screen.findByLabelText("IMDb 7,0 из 10")).toBeInTheDocument();
    expect(screen.queryByText("RT")).not.toBeInTheDocument();
    expect(infoRow("IMDb")).toBe("7,0 · 1 оценка");
    expect(infoRow("Rotten Tomatoes")).toBeUndefined();
  });

  it("shows nothing without ratings or without an IMDb id", async () => {
    const f = stub(movie, {});
    const { unmount } = renderItem();
    await screen.findByText("Космос");
    await vi.waitFor(() => expect(ratingCalls(f)).toHaveLength(1));
    expect(screen.queryByText("IMDb")).not.toBeInTheDocument();
    unmount();

    const g = stub(
      { ...movie, imdbId: undefined },
      { imdb: { rating: 9, votes: 9 } },
    );
    renderItem();
    await screen.findByText("Космос");
    expect(ratingCalls(g)).toHaveLength(0);
    expect(screen.queryByText("IMDb")).not.toBeInTheDocument();
  });

  it("formats in English", async () => {
    setLanguagePref("en");
    stub(movie, {
      imdb: { rating: 8.7, votes: 2312345 },
      rottenTomatoes: { tomatometer: 73 },
    });
    renderItem();
    const imdb = await screen.findByLabelText("IMDb 8.7 out of 10");
    expect(imdb.getAttribute("title")).toContain("2,312,345 votes");
    expect(infoRow("Rotten Tomatoes", "Information")).toBe("73%");
  });
});
