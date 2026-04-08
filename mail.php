<?php
if ($_SERVER["REQUEST_METHOD"] == "POST") {
    // Retrieve form inputs and sanitize them
    $name = htmlspecialchars($_POST['name']);
    $email = htmlspecialchars($_POST['email']);
    $number = htmlspecialchars($_POST['number']);
    $subject = htmlspecialchars($_POST['subject']);
    $message = htmlspecialchars($_POST['message']);

    // Email details
    $to = "sharun@techanise.com"; // To Email address
    $headers = "From: $email\r\n";
    $headers .= "Reply-To: $email\r\n";
    $headers .= "Content-Type: text/plain; charset=UTF-8\r\n";
    
    // Email body content
    $emailBody = "You have received a new message from your website contact form:\n\n";
    $emailBody .= "Name: $name\n";
    $emailBody .= "Email: $email\n";
    $emailBody .= "Phone: $number\n";
    $emailBody .= "Subject: $subject\n\n";
    $emailBody .= "Message:\n$message\n";

    // Send email and handle success or failure
    if (mail($to, $subject, $emailBody, $headers)) {
        echo "Message sent successfully!";
    } else {
        echo "Failed to send your message. Please try again later.";
    }
} else {
    echo "Invalid request method.";
}
?>
