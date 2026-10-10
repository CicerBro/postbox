import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import { ApiError } from "./lib/api";
import "./styles.css";
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: (failures, error) => !(error instanceof ApiError && error.status < 500) && error.name !== "AbortError" && failures < 1, refetchOnWindowFocus: false },
    mutations: { retry: false },
  },
});
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={queryClient}>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </QueryClientProvider>,
);
