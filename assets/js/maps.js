var map = L.map('map').setView([40.7128, -74.0060], 12);

// Using Google Maps tiles with Leaflet
L.tileLayer('https://{s}.google.com/vt/lyrs=m&x={x}&y={y}&z={z}', {
    subdomains: ['mt0', 'mt1', 'mt2', 'mt3']
}).addTo(map);

var marker = L.marker([40.7128, -74.0060]).addTo(map);

marker.bindPopup(`
    <div class="popup-content">
        <h3>New York Office</h3>
        <p>📍 123 5th Ave, New York, NY</p>
    </div>
`);
