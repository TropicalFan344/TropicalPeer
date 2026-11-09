// Web links inside a text from another device. They are found here and
// shown as separate pieces, never turned into HTML: the text is whatever
// the other device sent.

export interface Piece {
  text: string
  url?: string // set for a link: what opens (http or https only)
}

const found = /https?:\/\/[^\s<>"'`]+/gi
// Punctuation that ends a sentence rather than the address.
const trailing = /[.,;:!?'")\]}>]+$/

/** Splits body into plain text and http(s) links, in order. */
export function linkPieces(body: string): Piece[] {
  const out: Piece[] = []
  let at = 0
  for (const m of body.matchAll(found)) {
    let url = m[0]
    const cut = url.match(trailing)
    if (cut) {
      let tail = cut[0]
      // Keep a closing parenthesis the address opened: …/Rio_(city)
      while (tail.startsWith(')') && count(url, '(') > count(url.slice(0, url.length - tail.length), ')')) {
        tail = tail.slice(1)
      }
      url = url.slice(0, url.length - tail.length)
    }
    if (!/^https?:\/\/[^/?#\s]+/i.test(url)) continue // no host
    if (m.index > at) out.push({ text: body.slice(at, m.index) })
    out.push({ text: url, url })
    at = m.index + url.length
  }
  if (at < body.length) out.push({ text: body.slice(at) })
  return out
}

const count = (s: string, c: string) => s.split(c).length - 1
