export function save(token: string): void {
  window.sessionStorage.setItem('token', token);
}
