let videos = document.querySelectorAll('.video-item');
let showMoreButton = document.getElementById('show-more-btn');
let itemsToShow = 12; // Initially show 12 items

// Hide all video items after the first 12
function loadVideos() {
    let totalVideos = videos.length;
    
    // Loop through and display the videos based on the 'itemsToShow' value
    for (let i = 0; i < totalVideos; i++) {
        if (i < itemsToShow) {
            videos[i].style.display = 'block';
        } else {
            videos[i].style.display = 'none';
        }
    }
    
    // If all videos are shown, hide the "Show More" button
    if (itemsToShow >= totalVideos) {
        showMoreButton.style.display = 'none'; // Hide the "Show More" button
    }
}

// Event listener for "Show More" button
showMoreButton.addEventListener('click', () => {
    itemsToShow += 12; // Increase the number of videos to show by 12 each time
    loadVideos();
});

// Initial call to load the first 12 videos
loadVideos();
