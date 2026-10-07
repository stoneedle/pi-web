// Shared reactive session title. SessionHeader renders it and owns the
// document.title sync; CommandMenu (manual rename) and LiveReload (native-name
// SSE) update it through setSessionTitle instead of poking the
// #session-header-title element in the DOM directly.
export const sessionTitle = $state({ name: '' });

export function setSessionTitle(name) {
  const trimmed = String(name ?? '').trim();
  if (!trimmed) return;
  sessionTitle.name = trimmed;
}
