export function save(token: string): void {
  localStorage.setItem('token', token);
}
