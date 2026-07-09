import { Route, Routes } from "react-router-dom";

import Header from "./components/Header";
import HealthPage from "./pages/HealthPage";
import HomePage from "./pages/HomePage";
import LinkDetailPage from "./pages/LinkDetailPage";
import LoginPage from "./pages/LoginPage";
import SignupPage from "./pages/SignupPage";
import SubmitPage from "./pages/SubmitPage";
import UserPage from "./pages/UserPage";

export default function App() {
  return (
    <>
      <Header />
      <main className="container">
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/links/:id" element={<LinkDetailPage />} />
          <Route path="/submit" element={<SubmitPage />} />
          <Route path="/login" element={<LoginPage />} />
          <Route path="/signup" element={<SignupPage />} />
          <Route path="/users/:id" element={<UserPage />} />
          <Route path="/health" element={<HealthPage />} />
          <Route path="*" element={<p>ページが見つかりません。</p>} />
        </Routes>
      </main>
    </>
  );
}
