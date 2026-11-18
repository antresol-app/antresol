// scripts/lazy-player.mjs, the build's check that the video player stays
// out of the first load (npm run build runs it on the real manifest).
import { describe, expect, it } from 'vitest'
import { lazyPlayerProblems } from '../../../scripts/lazy-player.mjs'

const entry = { file: 'assets/index-a.js', isEntry: true, imports: ['_client-b.js'], dynamicImports: ['src/pages/Player.tsx', 'src/addons/loader.ts'] }
const lazy = {
  'index.html': entry,
  '_client-b.js': { file: 'assets/client-b.js' },
  'src/pages/Player.tsx': { file: 'assets/Player-c.js', imports: ['_VideoPlayer-d.js', 'index.html'] },
  '_VideoPlayer-d.js': { file: 'assets/VideoPlayer-d.js', imports: ['_client-b.js'] },
  'src/addons/loader.ts': { file: 'assets/loader-e.js', imports: ['index.html'], dynamicImports: ['src/player/VideoPlayer.tsx'] },
}

describe('lazy-player', () => {
  it('passes a build whose player is reached by dynamic imports only', () => {
    expect(lazyPlayerProblems(lazy)).toEqual([])
  })

  it('fails when the first load imports the player, directly or through a chunk', () => {
    const direct = { ...lazy, 'index.html': { ...entry, imports: ['_client-b.js', '_VideoPlayer-d.js'] } }
    expect(lazyPlayerProblems(direct)).toHaveLength(1)
    const through = { ...lazy, '_client-b.js': { file: 'assets/client-b.js', imports: ['src/player/VideoPlayer.tsx'] }, 'src/player/VideoPlayer.tsx': { file: 'assets/VideoPlayer-f.js' } }
    expect(lazyPlayerProblems(through)[0]).toMatch(/src\/player\/VideoPlayer\.tsx \(the video player\) is in the first load/)
  })

  it('fails when the player has no chunk of its own (merged away)', () => {
    const { ['_VideoPlayer-d.js']: _, ...merged } = lazy
    void _
    expect(lazyPlayerProblems(merged)).toEqual([expect.stringMatching(/no chunk of its own/)])
  })
})
