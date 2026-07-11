import { useEffect, useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";

import { api } from "../api";
import { useAuth } from "../auth";
import type { Tag } from "../types";
import { usePageTitle } from "../usePageTitle";

export default function SubmitPage() {
  const { user } = useAuth();
  const navigate = useNavigate();

  const [url, setUrl] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [imageURL, setImageURL] = useState("");
  const [tagsInput, setTagsInput] = useState("");
  const [allTags, setAllTags] = useState<Tag[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [fetching, setFetching] = useState(false);

  usePageTitle("リンクを投稿");

  // 既存タグをサジェスト用に読み込む（失敗しても投稿には影響しない）
  useEffect(() => {
    api
      .listTags()
      .then((res) => setAllTags(res.tags ?? []))
      .catch(() => {});
  }, []);

  if (!user) return <Navigate to="/login" replace />;

  // 入力中の最後のトークン（区切り文字で終わっていれば空 = 新しいタグの入力待ち）
  const endsWithSep = /[,\s]$/.test(tagsInput);
  const tokens = tagsInput
    .split(/[,\s]+/)
    .map((t) => t.trim().toLowerCase())
    .filter(Boolean);
  const editing = endsWithSep || tokens.length === 0 ? "" : tokens[tokens.length - 1];
  const chosen = new Set(editing ? tokens.slice(0, -1) : tokens);
  const suggestions = allTags
    .filter(
      (t) =>
        !chosen.has(t.name) &&
        t.name !== editing &&
        (editing === "" || t.name.startsWith(editing)),
    )
    .slice(0, 8);

  // サジェストされたタグで入力中のトークンを補完（または末尾に追加）する
  const pickTag = (name: string) => {
    const base = editing
      ? tagsInput.replace(/[^,\s]+$/, "")
      : tagsInput && !endsWithSep
        ? tagsInput + " "
        : tagsInput;
    setTagsInput(base + name + " ");
  };

  // URL から OGP を取得してタイトル・説明・サムネイルをプリフィルする
  const prefill = async () => {
    if (!url.trim()) return;
    setFetching(true);
    try {
      const ogp = await api.fetchOGP(url.trim());
      if (ogp.title && !title) setTitle(ogp.title.slice(0, 200));
      if (ogp.description && !description)
        setDescription(ogp.description.slice(0, 1000));
      if (ogp.image && !imageURL) setImageURL(ogp.image.slice(0, 2000));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setFetching(false);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const tags = tagsInput
      .split(/[,\s]+/)
      .map((t) => t.trim().toLowerCase())
      .filter(Boolean);
    setBusy(true);
    try {
      const res = await api.createLink({
        url: url.trim(),
        title: title.trim(),
        description: description.trim(),
        image_url: imageURL.trim(),
        tags,
      });
      navigate(`/links/${res.link.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <div className="form-page">
      <h1>リンクを投稿</h1>
      <form onSubmit={submit}>
        <label>
          URL
          <div className="url-row">
            <input
              type="url"
              required
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onBlur={prefill}
              placeholder="https://example.com/article"
            />
            <button
              type="button"
              className="button small"
              onClick={prefill}
              disabled={fetching || !url.trim()}
            >
              {fetching ? "取得中…" : "情報取得"}
            </button>
          </div>
        </label>
        <label>
          タイトル
          <input
            required
            maxLength={200}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </label>
        <label>
          説明（任意）
          <textarea
            rows={4}
            maxLength={1000}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
        <label>
          タグ（スペース・カンマ区切り、最大 5 個。英小文字・数字・-_）
          <input
            value={tagsInput}
            onChange={(e) => setTagsInput(e.target.value)}
            placeholder="go raft distributed-systems"
          />
        </label>
        {suggestions.length > 0 && (
          <div className="tag-suggest">
            {suggestions.map((t) => (
              <button
                key={t.name}
                type="button"
                className="tag-suggest-item"
                onClick={() => pickTag(t.name)}
              >
                {t.name} <span className="count">{t.count}</span>
              </button>
            ))}
          </div>
        )}
        {imageURL && (
          <div className="thumb-preview">
            <img
              src={imageURL}
              alt="サムネイルプレビュー"
              onError={() => setImageURL("")}
            />
            <button
              type="button"
              className="button ghost small"
              onClick={() => setImageURL("")}
            >
              サムネイルを外す
            </button>
          </div>
        )}
        {error && <p className="error">{error}</p>}
        <button className="button primary" disabled={busy}>
          {busy ? "投稿中…" : "投稿する"}
        </button>
      </form>
    </div>
  );
}
