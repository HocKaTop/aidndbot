let token = "";
export const setToken = (value: string) => {
  token = value;
};
export const getToken = () => token;
export async function api<T>(
  path: string,
  method = "GET",
  data?: unknown,
): Promise<T> {
  const response = await fetch("/api" + path, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: "Bearer " + token } : {}),
    },
    body: data === undefined ? undefined : JSON.stringify(data),
  });
  const body = await response.json();
  if (!response.ok)
    throw new Error(body.error?.message || "Не удалось связаться с сервером");
  return body as T;
}
