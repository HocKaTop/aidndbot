let token = "";
export const setToken = (value: string) => {
  token = value;
};
export const getToken = () => token;
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code: string,
  ) {
    super(message);
  }
}
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
  const body = await response.json().catch(() => null);
  if (!response.ok)
    throw new ApiError(
      body?.error?.message || "Сервер временно недоступен. Попробуй ещё раз.",
      response.status,
      body?.error?.code || "HTTP_ERROR",
    );
  if (body === null) throw new Error("Сервер вернул некорректный ответ");
  return body as T;
}
