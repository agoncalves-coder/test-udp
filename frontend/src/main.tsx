import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'

// Sin StrictMode: su doble-mount en dev arrancaría getUserMedia dos veces y
// pelearía por la cámara; el loop de captura igual vive fuera de React.
createRoot(document.getElementById('root')!).render(<App />)
