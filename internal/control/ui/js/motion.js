// motion.js — small shared primitives for state-driven UI motion. Keep this
// module deliberately narrow: CSS owns repeatable component states; these
// helpers cover one-shot changes and optional main-panel View Transitions.
export const MOTION = Object.freeze({
  instant: 80,
  fast: 120,
  base: 180,
  slow: 260,
  standard: 'cubic-bezier(.4,0,.2,1)',
  exit: 'cubic-bezier(.4,0,1,1)',
  enter: 'cubic-bezier(.2,.8,.2,1)',
});

const reduced = window.matchMedia('(prefers-reduced-motion: reduce)');

export function prefersReducedMotion(){
  return reduced.matches;
}

export function cancelElementAnimations(element){
  if(!element || typeof element.getAnimations !== 'function') return;
  element.getAnimations().forEach(animation => animation.cancel());
}

export function animateOnce(element, keyframes, options={}){
  if(!element || document.hidden || prefersReducedMotion() || typeof element.animate !== 'function'){
    return Promise.resolve();
  }
  cancelElementAnimations(element);
  const animation = element.animate(keyframes, {
    duration: MOTION.base,
    easing: MOTION.standard,
    ...options,
  });
  return animation.finished.catch(()=>{});
}

export function transitionView(updateFunction){
  // Tab semantics are synchronous: a deferred View Transition callback can let
  // rapid click/arrow activation leave focus on one tab while another panel is
  // still active. Commit first, then animate only the newly-active panel.
  updateFunction();
  if(document.hidden || prefersReducedMotion()) return null;
  const panel=document.querySelector('.panel.active');
  if(!panel || typeof panel.animate !== 'function') return null;
  cancelElementAnimations(panel);
  const x=document.documentElement.dataset.motionDirection==='back'?-6:6;
  return panel.animate([
    {opacity:.72,transform:`translateX(${x}px)`},
    {opacity:1,transform:'translateX(0)'},
  ],{duration:MOTION.base,easing:MOTION.enter});
}
