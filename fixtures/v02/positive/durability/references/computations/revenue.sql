SELECT SUM(amount) AS revenue
FROM finance.recognized_revenue
WHERE fiscal_year = :year;
