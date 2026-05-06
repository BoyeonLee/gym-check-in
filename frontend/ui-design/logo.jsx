// P-BOY MMA Logo wrapper — uses the original logo image
// (do not redraw the brand mark in SVG; it's a real logo asset)

const PBoyLogo = ({ size = 80, variant = 'dark' }) => {
  // variant: 'dark' (logo on dark) or 'inverted' (just the mark, no bg)
  return (
    <div style={{
      width: size,
      height: size,
      display: 'inline-flex',
      alignItems: 'center',
      justifyContent: 'center',
      backgroundColor: variant === 'dark' ? '#000' : 'transparent',
      borderRadius: 4,
      overflow: 'hidden',
      flexShrink: 0,
    }}>
      <img
        src="assets/pboy-logo.jpg"
        alt="P-BOY MMA"
        style={{ width: '100%', height: '100%', objectFit: 'contain' }}
      />
    </div>
  );
};

// Wordmark only (for places where logo image is too busy)
const PBoyWordmark = ({ color = '#FAFAFA', size = 18, accent = '#E10600' }) => (
  <div style={{
    fontFamily: 'Space Grotesk, sans-serif',
    fontWeight: 700,
    fontSize: size,
    letterSpacing: size * 0.04,
    lineHeight: 1,
    color,
    display: 'inline-flex',
    alignItems: 'center',
    gap: size * 0.4,
  }}>
    <span style={{
      width: size * 0.36,
      height: size * 0.9,
      backgroundColor: accent,
      display: 'inline-block',
    }}/>
    <span>P-BOY <span style={{ color: accent }}>MMA</span></span>
  </div>
);

window.PBoyLogo = PBoyLogo;
window.PBoyWordmark = PBoyWordmark;
