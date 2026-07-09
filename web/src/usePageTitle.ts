import { useEffect } from "react";

// ページごとにブラウザタブのタイトルを設定する。
export function usePageTitle(title: string) {
  useEffect(() => {
    document.title = title ? `${title} - LinkRaft` : "LinkRaft";
    return () => {
      document.title = "LinkRaft";
    };
  }, [title]);
}
