import { Navigate } from "react-router-dom";

// Users management moved into Settings (admin only); keep the old URL working.
export default function UsersPage() {
  return <Navigate to="/settings?tab=users" replace />;
}
