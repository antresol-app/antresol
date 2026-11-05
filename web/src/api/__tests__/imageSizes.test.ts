import { describe, expect, it } from 'vitest'
import { imageSrcSet, imageUrl } from '../client'

describe('image sizes', () => {
  it('asks the server for the smallest step at least as wide', () => {
    expect(imageUrl('0123abcd.jpg', 300)).toBe('/api/images/0123abcd.jpg?w=320')
    expect(imageUrl('/api/images/local/5/backdrop', 1280)).toBe('/api/images/local/5/backdrop?w=1280')
    expect(imageUrl('0123abcd.jpg')).toBe('/api/images/0123abcd.jpg')
    // wider than the last step: the original
    expect(imageUrl('0123abcd.jpg', 4000)).toBe('/api/images/0123abcd.jpg')
  })

  it('leaves other URLs as they are', () => {
    expect(imageUrl('https://image.tmdb.org/t/p/w500/x.jpg', 320)).toBe('https://image.tmdb.org/t/p/w500/x.jpg')
    expect(imageUrl('blob:abc', 320)).toBe('blob:abc')
    expect(imageSrcSet('https://image.tmdb.org/t/p/w500/x.jpg')).toBeUndefined()
    expect(imageSrcSet(undefined)).toBeUndefined()
  })

  it('lists the steps between min and max', () => {
    expect(imageSrcSet('0123abcd.jpg', 320, 640)).toBe('/api/images/0123abcd.jpg?w=320 320w, /api/images/0123abcd.jpg?w=480 480w, /api/images/0123abcd.jpg?w=640 640w')
  })
})
