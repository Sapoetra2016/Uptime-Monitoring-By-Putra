import { Navigate } from "react-router-dom";

// Notifications moved into Settings; keep the old URL working.
export default function NotificationsPage() {
  return <Navigate to="/settings?tab=notifications" replace />;
}
