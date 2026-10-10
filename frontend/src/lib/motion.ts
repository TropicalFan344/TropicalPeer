// The app's motion vocabulary: one exponential ease-out, short durations, and
// a reduced-motion path that keeps state changes legible without movement.

/** cubic-bezier(0.16, 1, 0.3, 1) as an easing function: the --ease-out-expo of app.css. */
export const easeOutExpo = (x: number) => (x === 1 ? 1 : 1 - Math.pow(2, -10 * x))

const query = typeof matchMedia !== 'undefined' ? matchMedia('(prefers-reduced-motion: reduce)') : null

/** True when the person asked the system for less motion. */
export const reducedMotion = () => query?.matches ?? false

/** A duration that collapses to nothing under reduced motion. */
export const dur = (ms: number) => (reducedMotion() ? 0 : ms)
