import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";

import { api } from "../api";
import LinkCard from "../components/LinkCard";
import type { LinkListResponse } from "../types";

export default function HomePage() {
  const [params, setParams] = useSearchParams();
  const sort = params.get("sort") === "popular" ? "popular" : "recent";
  const tag = params.get("tag") ?? "";
  const q = params.get("q") ?? "";
  const page = Math.max(1, Number(params.get("page")) || 1);

  const [data, setData] = useState<LinkListResponse | null>(null);
  const [error, setError] = useState("");
  const [searchInput, setSearchInput] = useState(q);

  useEffect(() => {
    setSearchInput(q);
  }, [q]);

  useEffect(() => {
    let cancelled = false;
    api
      .listLinks({ sort, tag: tag || undefined, q: q || undefined, page })
      .then((res) => {
        if (!cancelled) {
          setData(res);
          setError("");
        }
      })
      .catch((e) => {
        if (!cancelled) setError(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [sort, tag, q, page]);

  const update = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    setParams(next);
  };

  const onSearch = (e: FormEvent) => {
    e.preventDefault();
    update({ q: searchInput.trim(), page: "" });
  };

  const totalPages = data ? Math.max(1, Math.ceil(data.total / data.per_page)) : 1;

  return (
    <div>
      <div className="toolbar">
        <div className="sort-tabs">
          <button
            className={sort === "recent" ? "tab active" : "tab"}
            onClick={() => update({ sort: "", page: "" })}
          >
            新着
          </button>
          <button
            className={sort === "popular" ? "tab active" : "tab"}
            onClick={() => update({ sort: "popular", page: "" })}
          >
            人気
          </button>
        </div>
        <form onSubmit={onSearch} className="search-form">
          <input
            type="search"
            placeholder="タイトル・URL を検索"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
          />
        </form>
      </div>

      {tag && (
        <p className="filter-note">
          タグ <span className="tag">{tag}</span> で絞り込み中{" "}
          <button className="button ghost small" onClick={() => update({ tag: "", page: "" })}>
            解除
          </button>
        </p>
      )}

      {error && <p className="error">{error}</p>}
      {data && data.links.length === 0 && <p>リンクがまだありません。</p>}
      {data?.links.map((l) => (
        <LinkCard
          key={l.id}
          link={l}
          onVoted={(id, count, voted) =>
            setData((prev) =>
              prev
                ? {
                    ...prev,
                    links: prev.links.map((x) =>
                      x.id === id ? { ...x, vote_count: count, voted } : x,
                    ),
                  }
                : prev,
            )
          }
        />
      ))}

      {data && totalPages > 1 && (
        <div className="pagination">
          <button
            className="button small"
            disabled={page <= 1}
            onClick={() => update({ page: String(page - 1) })}
          >
            ← 前
          </button>
          <span>
            {page} / {totalPages}
          </span>
          <button
            className="button small"
            disabled={page >= totalPages}
            onClick={() => update({ page: String(page + 1) })}
          >
            次 →
          </button>
        </div>
      )}
    </div>
  );
}
