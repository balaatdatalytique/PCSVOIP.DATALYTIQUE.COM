document.addEventListener("DOMContentLoaded", function () {
  // Select the container that holds the table.
  const tableContainer = document.querySelector(".pt-container");
  // Select table rows from the table body of our custom table.
  const tableBody = document.querySelector("#pt-table tbody");
  const rows = tableBody.getElementsByTagName("tr");
  const rowsPerPage = 10;
  const paginationContainer = document.getElementById("pt-pagination");
  let currentPage = 1;
  const totalPages = Math.ceil(rows.length / rowsPerPage);

  // Function to display rows for the given page.
  function displayRows(page) {
    currentPage = page;
    const start = (page - 1) * rowsPerPage;
    const end = start + rowsPerPage;

    // Hide all rows.
    for (let i = 0; i < rows.length; i++) {
      rows[i].style.display = "none";
    }

    // Show only the rows for the current page.
    for (let i = start; i < end && i < rows.length; i++) {
      rows[i].style.display = "";
    }
    updatePagination();

    // Scroll the table container into view after updating rows.
    tableContainer.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  // Function to update the pagination controls.
  function updatePagination() {
    paginationContainer.innerHTML = "";
    for (let i = 1; i <= totalPages; i++) {
      const pageSpan = document.createElement("span");
      pageSpan.textContent = i;
      pageSpan.className = "pt-page";
      if (i === currentPage) {
        pageSpan.classList.add("active");
      }
      pageSpan.addEventListener("click", function () {
        displayRows(i);
      });
      paginationContainer.appendChild(pageSpan);
    }
  }

  // Initialize the table by displaying the first page.
  displayRows(1);
});
