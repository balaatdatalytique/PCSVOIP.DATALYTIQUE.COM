document.getElementById('contactForm').addEventListener('submit', function (e) {
    e.preventDefault(); // Prevent default form submission

    const formData = new FormData(this);

    // Send AJAX request
    fetch('mail.php', {
        method: 'POST',
        body: formData,
    })
        .then(response => response.json())
        .then(data => {
            const popup = document.getElementById('formResponse');
            const message = document.getElementById('responseMessage');
            message.textContent = data.message;

            // Show the popup
            popup.style.display = 'flex';

            // Close the popup when the close button is clicked
            document.querySelector('.close-btn').onclick = function () {
                popup.style.display = 'none';
            };
        })
        .catch(error => {
            console.error('Error:', error);
        });
});
