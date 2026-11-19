function draw(container, ctx, phase) {
  const flow = ctx.getSelectedFlow();
  container.textContent = `${phase}:${ctx.tabId}/${ctx.side || ctx.menuId}:${ctx.esc(`<${flow.label}>`)}`;
}

export async function mount(container, ctx) {
  const flow = ctx.flow;
  if (flow?.wait) {
    container.events = flow.events;
    ctx.onSse('fixture', () => {});
    await flow.wait();
    ctx.onSse('fixture', () => {});
  }
  draw(container, ctx, 'mount');
}

export async function update(container, ctx) {
  if (ctx.flow?.wait) await ctx.flow.wait();
  draw(container, ctx, 'update');
}

export function unmount(container) {
  container.events?.push('unmount');
}

export async function run(ctx) {
  ctx.flow.rendered = `entry:${ctx.esc(`<${ctx.flow.label}>`)}`;
  ctx.onSse('fixture', () => {});
  ctx.flow.record();
  throw new Error('fixture error');
}
