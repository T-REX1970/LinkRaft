// API の英語エラーメッセージを表示用の日本語に変換する。

const messages: Record<string, string> = {
  "invalid request body": "リクエストの形式が不正です",
  "url must be a valid http(s) URL": "URL は http(s) で始まる有効な URL を入力してください",
  "image_url must be a valid http(s) URL": "画像 URL が不正です",
  "title must be 1-200 characters": "タイトルは 1〜200 文字で入力してください",
  "description must be at most 1000 characters": "説明は 1000 文字以内で入力してください",
  "at most 5 tags": "タグは 5 個までです",
  "name must be 1-50 characters": "名前は 1〜50 文字で入力してください",
  "invalid email address": "メールアドレスの形式が不正です",
  "password must be 8-72 characters": "パスワードは 8〜72 文字で入力してください",
  "email already registered": "このメールアドレスは既に登録されています",
  "invalid email or password": "メールアドレスまたはパスワードが違います",
  "missing bearer token": "ログインが必要です",
  "invalid token": "ログインの有効期限が切れました",
  "link not found": "リンクが見つかりません",
  "comment not found": "コメントが見つかりません",
  "user not found": "ユーザーが見つかりません",
  "parent comment not found": "返信先のコメントが見つかりません",
  "replies are limited to one level": "返信への返信はできません",
  "only the owner can delete this link": "このリンクを削除できるのは投稿者本人だけです",
  "only the owner can delete this comment": "このコメントを削除できるのは投稿者本人だけです",
  "body must be 1-2000 characters": "コメントは 1〜2000 文字で入力してください",
  "failed to fetch url": "URL の取得に失敗しました",
  "target returned non-2xx status": "URL の取得に失敗しました（相手サーバーがエラーを返しました）",
};

export function translateError(message: string, status: number): string {
  if (messages[message]) return messages[message];
  if (message.startsWith("invalid tag: ")) {
    return `不正なタグです: ${message.slice("invalid tag: ".length)}（英小文字・数字・ハイフン・アンダースコアのみ）`;
  }
  if (status >= 500) return "サーバーエラーが発生しました。しばらくしてから再度お試しください";
  return message || "エラーが発生しました";
}
