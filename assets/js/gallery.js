document.addEventListener("DOMContentLoaded", function() {
  const galleryItems = document.querySelectorAll('.gallery-item');
  const lightbox = document.getElementById('lightbox');
  const lightboxContent = document.querySelector('.lightbox-content');
  const closeBtn = document.querySelector('.close');

  // Open lightbox on thumbnail click
  galleryItems.forEach(item => {
    item.addEventListener('click', function() {
      lightbox.style.display = 'block';
      lightboxContent.src = this.src;
    });
  });

  // Close lightbox when clicking the close button
  closeBtn.addEventListener('click', function() {
    lightbox.style.display = 'none';
  });

  // Also close lightbox if clicking outside the image
  lightbox.addEventListener('click', function(e) {
    if (e.target === lightbox) {
      lightbox.style.display = 'none';
    }
  });
});
