import { Navigate } from "react-router-dom";

// Status pages moved into Settings; keep the old URL working.
export default function StatusPagesPage() {
  return <Navigate to="/settings?tab=statusPages" replace />;
}
