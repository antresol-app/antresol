// «Статистика»: the line under the library says what transcodes video.
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import type { Stats as Data } from "../../../api/types";
import { session } from "../../../state/session";
import { Stats } from "../Stats";

const json = (b: unknown) =>
  new Response(JSON.stringify(b), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const base: Data = {
  disk: { total: 0, used: 0, free: 0, categories: [] },
  counts: {
    movies: 1,
    series: 0,
    episodes: 0,
    albums: 0,
    tracks: 0,
    books: 0,
    audiobooks: 0,
    comics: 0,
  },
  watchedByUser: [],
  topItems30d: [],
};

const show = (stats: Data) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json(stats)),
  );
  return render(
    <MemoryRouter>
      <Stats />
    </MemoryRouter>,
  );
};

afterEach(() => {
  vi.unstubAllGlobals();
  session.reset();
});

describe("transcoding line", () => {
  it("names the GPU backend", async () => {
    show({
      ...base,
      transcoding: {
        mode: "vaapi",
        name: "VA-API",
        requested: "auto",
        device: "/dev/dri/renderD128",
      },
    });
    expect(
      await screen.findByText("Перекодирование видео: видеокарта, VA-API"),
    ).toBeInTheDocument();
  });
  it("says CPU in software mode", async () => {
    show({
      ...base,
      transcoding: { mode: "none", name: "software", requested: "auto" },
    });
    expect(
      await screen.findByText("Перекодирование видео: процессор"),
    ).toBeInTheDocument();
  });
  it("shows nothing when the server does not say", async () => {
    show(base);
    await screen.findByText("Статистика");
    expect(screen.queryByText(/Перекодирование видео/)).toBeNull();
  });
});
